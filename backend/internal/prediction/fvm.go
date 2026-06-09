package prediction

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	"lng-monitor/internal/config"
	"lng-monitor/internal/database"
	"lng-monitor/internal/models"
)

const (
	NumLayers           = 5
	LNGSpecificHeat     = 3.47
	LNGThermalConduct   = 0.19
	LNGKinematicVisc    = 2.7e-7
	GravitationalAccel  = 9.81
	LNGThermalExpansion = 3.6e-3
	ModelVersion        = "fvm-v1"
)

type FiniteVolumeModel struct {
	cfg *config.Config
	db  *database.DB

	layerTemp     [NumLayers]float64
	layerDensity  [NumLayers]float64
	layerHeight   [NumLayers]float64

	previousTemp    [NumLayers]float64
	previousDensity [NumLayers]float64

	timeStep       float64
	tankHeight     float64
	tankVolume     float64

	tempHistory    [][]float64
	densityHistory [][]float64
}

type PredictionResult struct {
	RiskIndex          float64
	StabilityScore     float64
	PredictedRollover  *time.Time
	MaxTempGradient    float64
	MaxDensityGradient float64
}

func NewFiniteVolumeModel(cfg *config.Config, db *database.DB) *FiniteVolumeModel {
	tankHeight := 38.0
	layerH := tankHeight / NumLayers

	fvm := &FiniteVolumeModel{
		cfg:        cfg,
		db:         db,
		tankHeight: tankHeight,
		tankVolume: 160000.0,
		timeStep:   float64(cfg.DataIntervalSec),
	}

	for i := 0; i < NumLayers; i++ {
		fvm.layerHeight[i] = layerH
		fvm.layerTemp[i] = -162.0 + float64(i)*0.5
		fvm.layerDensity[i] = 450.0 + float64(i)*3.0
		fvm.previousTemp[i] = fvm.layerTemp[i]
		fvm.previousDensity[i] = fvm.layerDensity[i]
	}

	fvm.tempHistory = make([][]float64, 0)
	fvm.densityHistory = make([][]float64, 0)

	return fvm
}

func (fvm *FiniteVolumeModel) Start(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(fvm.cfg.PredictionIntervalSec) * time.Second)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				fvm.runPredictionCycle(ctx)
			}
		}
	}()
	log.Println("rollover prediction model started")
}

func (fvm *FiniteVolumeModel) runPredictionCycle(ctx context.Context) {
	tanks, err := fvm.db.GetTanks(ctx)
	if err != nil {
		log.Printf("prediction: get tanks error: %v", err)
		return
	}

	for _, tank := range tanks {
		result := fvm.predict(ctx, tank.ID)

		prediction := models.RolloverPrediction{
			Time:               time.Now().UTC(),
			TankID:             tank.ID,
			RiskIndex:          result.RiskIndex,
			LayerStabilityScore: result.StabilityScore,
			PredictedRolloverTime: result.PredictedRollover,
			MaxTempGradient:    result.MaxTempGradient,
			MaxDensityGradient: result.MaxDensityGradient,
			ModelVersion:       ModelVersion,
		}

		if err := fvm.db.InsertPrediction(ctx, prediction); err != nil {
			log.Printf("prediction: insert error tank=%d: %v", tank.ID, err)
		}

		log.Printf("prediction: tank=%s risk=%.3f stability=%.3f", tank.TankCode, result.RiskIndex, result.StabilityScore)
	}
}

func (fvm *FiniteVolumeModel) predict(ctx context.Context, tankID int) PredictionResult {
	temps, err := fvm.db.GetLatestTemperatures(ctx, tankID)
	if err != nil || len(temps) == 0 {
		log.Printf("prediction: no temp data for tank %d, using defaults", tankID)
		return fvm.computePrediction(tankID)
	}

	densities, _ := fvm.db.GetLatestDensities(ctx, tankID)

	fvm.previousTemp = fvm.layerTemp
	fvm.previousDensity = fvm.layerDensity

	layerTempAvg := make(map[int]float64)
	layerTempCount := make(map[int]int)
	for _, t := range temps {
		layerTempAvg[t.LayerIndex] += t.ValueCelsius
		layerTempCount[t.LayerIndex]++
	}
	for k, v := range layerTempAvg {
		idx := k - 1
		if idx >= 0 && idx < NumLayers {
			fvm.layerTemp[idx] = v / float64(layerTempCount[k])
		}
	}

	layerDensityAvg := make(map[int]float64)
	layerDensityCount := make(map[int]int)
	for _, d := range densities {
		layerDensityAvg[d.LayerIndex] += d.ValueKgM3
		layerDensityCount[d.LayerIndex]++
	}
	for k, v := range layerDensityAvg {
		idx := k - 1
		if idx >= 0 && idx < NumLayers {
			fvm.layerDensity[idx] = v / float64(layerDensityCount[k])
		}
	}

	tempSlice := make([]float64, NumLayers)
	densSlice := make([]float64, NumLayers)
	copy(tempSlice, fvm.layerTemp[:])
	copy(densSlice, fvm.layerDensity[:])
	fvm.tempHistory = append(fvm.tempHistory, tempSlice)
	fvm.densityHistory = append(fvm.densityHistory, densSlice)
	if len(fvm.tempHistory) > 2880 {
		fvm.tempHistory = fvm.tempHistory[1:]
		fvm.densityHistory = fvm.densityHistory[1:]
	}

	return fvm.computePrediction(tankID)
}

func (fvm *FiniteVolumeModel) computePrediction(tankID int) PredictionResult {
	maxTempGrad := 0.0
	maxDensGrad := 0.0

	for i := 0; i < NumLayers-1; i++ {
		tempGrad := math.Abs(fvm.layerTemp[i+1]-fvm.layerTemp[i]) / fvm.layerHeight[i]
		densGrad := math.Abs(fvm.layerDensity[i+1]-fvm.layerDensity[i]) / fvm.layerHeight[i]

		if tempGrad > maxTempGrad {
			maxTempGrad = tempGrad
		}
		if densGrad > maxDensGrad {
			maxDensGrad = densGrad
		}
	}

	maxLayerTempDiff := 0.0
	maxLayerDensDiff := 0.0
	minTemp := math.Inf(1)
	maxTemp := math.Inf(-1)
	minDens := math.Inf(1)
	maxDens := math.Inf(-1)

	for i := 0; i < NumLayers; i++ {
		for j := i + 1; j < NumLayers; j++ {
			td := math.Abs(fvm.layerTemp[i] - fvm.layerTemp[j])
			dd := math.Abs(fvm.layerDensity[i] - fvm.layerDensity[j])
			if td > maxLayerTempDiff {
				maxLayerTempDiff = td
			}
			if dd > maxLayerDensDiff {
				maxLayerDensDiff = dd
			}
		}
		if fvm.layerTemp[i] < minTemp {
			minTemp = fvm.layerTemp[i]
		}
		if fvm.layerTemp[i] > maxTemp {
			maxTemp = fvm.layerTemp[i]
		}
		if fvm.layerDensity[i] < minDens {
			minDens = fvm.layerDensity[i]
		}
		if fvm.layerDensity[i] > maxDens {
			maxDens = fvm.layerDensity[i]
		}
	}

	raTemp := fvm.computeRayleighNumber(maxLayerTempDiff)
	raDens := fvm.computeRayleighNumberDensity(maxLayerDensDiff)

	raCritical := 1e8
	raRatio := 0.0
	if raCritical > 0 {
		raRatio = math.Max(raTemp, raDens) / raCritical
	}

	tempContrib := math.Min(maxLayerTempDiff/8.0, 1.0)
	densContrib := math.Min(maxLayerDensDiff/2.0, 1.0)
	riskIndex := 0.4*tempContrib + 0.4*densContrib + 0.2*math.Min(raRatio, 1.0)
	riskIndex = math.Min(riskIndex, 1.0)

	stabilityScore := 1.0 - riskIndex

	fvm.solveHeatDiffusion()
	fvm.solveMassDiffusion()

	driftRate := fvm.computeInstabilityDriftRate()
	var predictedRollover *time.Time
	if riskIndex > 0.3 && driftRate > 0 {
		hoursToRollover := (0.8 - riskIndex) / driftRate
		if hoursToRollover > 0 {
			t := time.Now().UTC().Add(time.Duration(hoursToRollover * float64(time.Hour)))
			predictedRollover = &t
		}
	}

	return PredictionResult{
		RiskIndex:          riskIndex,
		StabilityScore:     stabilityScore,
		PredictedRollover:  predictedRollover,
		MaxTempGradient:    maxLayerTempDiff,
		MaxDensityGradient: maxLayerDensDiff,
	}
}

func (fvm *FiniteVolumeModel) computeRayleighNumber(deltaT float64) float64 {
	if deltaT <= 0 {
		return 0
	}
	charLen := fvm.tankHeight
	beta := LNGThermalExpansion
	alpha := LNGThermalConduct / (fvm.layerDensity[2] * LNGSpecificHeat)
	nu := LNGKinematicVisc
	ra := GravitationalAccel * beta * deltaT * math.Pow(charLen, 3) / (nu * alpha)
	return ra
}

func (fvm *FiniteVolumeModel) computeRayleighNumberDensity(deltaRho float64) float64 {
	if deltaRho <= 0 {
		return 0
	}
	charLen := fvm.tankHeight
	rhoRef := fvm.layerDensity[2]
	nu := LNGKinematicVisc
	alpha := LNGThermalConduct / (rhoRef * LNGSpecificHeat)
	ra := GravitationalAccel * deltaRho / rhoRef * math.Pow(charLen, 3) / (nu * alpha)
	return ra
}

func (fvm *FiniteVolumeModel) solveHeatDiffusion() {
	dt := fvm.timeStep
	k := LNGThermalConduct
	rho := fvm.layerDensity[2]
	cp := LNGSpecificHeat
	alpha := k / (rho * cp)

	newTemp := fvm.layerTemp

	for i := 1; i < NumLayers-1; i++ {
		dx := fvm.layerHeight[i]
		d2Tdx2 := (fvm.layerTemp[i+1] - 2*fvm.layerTemp[i] + fvm.layerTemp[i-1]) / (dx * dx)
		newTemp[i] = fvm.layerTemp[i] + alpha*dt*d2Tdx2
	}

	dx := fvm.layerHeight[0]
	d2Tdx2 := (fvm.layerTemp[1] - fvm.layerTemp[0]) / (dx * dx)
	newTemp[0] = fvm.layerTemp[0] + alpha*dt*d2Tdx2*0.5

	dx = fvm.layerHeight[NumLayers-1]
	d2Tdx2 = (fvm.layerTemp[NumLayers-2] - fvm.layerTemp[NumLayers-1]) / (dx * dx)
	newTemp[NumLayers-1] = fvm.layerTemp[NumLayers-1] + alpha*dt*d2Tdx2*0.5

	fvm.layerTemp = newTemp
}

func (fvm *FiniteVolumeModel) solveMassDiffusion() {
	dt := fvm.timeStep
	dEff := 1e-9

	newDensity := fvm.layerDensity

	for i := 1; i < NumLayers-1; i++ {
		dx := fvm.layerHeight[i]
		d2Rhodx2 := (fvm.layerDensity[i+1] - 2*fvm.layerDensity[i] + fvm.layerDensity[i-1]) / (dx * dx)
		newDensity[i] = fvm.layerDensity[i] + dEff*dt*d2Rhodx2
	}

	dx := fvm.layerHeight[0]
	d2Rhodx2 := (fvm.layerDensity[1] - fvm.layerDensity[0]) / (dx * dx)
	newDensity[0] = fvm.layerDensity[0] + dEff*dt*d2Rhodx2*0.5

	dx = fvm.layerHeight[NumLayers-1]
	d2Rhodx2 = (fvm.layerDensity[NumLayers-2] - fvm.layerDensity[NumLayers-1]) / (dx * dx)
	newDensity[NumLayers-1] = fvm.layerDensity[NumLayers-1] + dEff*dt*d2Rhodx2*0.5

	fvm.layerDensity = newDensity
}

func (fvm *FiniteVolumeModel) computeInstabilityDriftRate() float64 {
	if len(fvm.tempHistory) < 10 {
		return 0
	}

	n := len(fvm.tempHistory)
	recentRisk := make([]float64, 0, 10)
	window := 10
	if n < window {
		window = n
	}

	for i := n - window; i < n; i++ {
		maxTD := 0.0
		maxDD := 0.0
		for j := 0; j < NumLayers; j++ {
			for k := j + 1; k < NumLayers; k++ {
				td := math.Abs(fvm.tempHistory[i][j] - fvm.tempHistory[i][k])
				dd := math.Abs(fvm.densityHistory[i][j] - fvm.densityHistory[i][k])
				if td > maxTD {
					maxTD = td
				}
				if dd > maxDD {
					maxDD = dd
				}
			}
		}
		tc := math.Min(maxTD/8.0, 1.0)
		dc := math.Min(maxDD/2.0, 1.0)
		recentRisk = append(recentRisk, 0.5*tc+0.5*dc)
	}

	if len(recentRisk) < 2 {
		return 0
	}

	driftSum := 0.0
	for i := 1; i < len(recentRisk); i++ {
		driftSum += recentRisk[i] - recentRisk[i-1]
	}

	return driftSum / float64(len(recentRisk)-1) / float64(fvm.cfg.PredictionIntervalSec) * 3600
}

func (fvm *FiniteVolumeModel) GetLayerData(tankID int) (temps [NumLayers]float64, densities [NumLayers]float64) {
	return fvm.layerTemp, fvm.layerDensity
}

func FormatPrediction(p PredictionResult) string {
	rollover := "N/A"
	if p.PredictedRollover != nil {
		rollover = p.PredictedRollover.Format("2006-01-02 15:04:05")
	}
	return fmt.Sprintf("risk=%.3f stability=%.3f rollover=%s tempGrad=%.2f densGrad=%.2f",
		p.RiskIndex, p.StabilityScore, rollover, p.MaxTempGradient, p.MaxDensityGradient)
}
