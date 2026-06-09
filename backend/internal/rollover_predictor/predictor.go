package rollover_predictor

import (
	"context"
	"log"
	"math"
	"sync"
	"time"

	"lng-monitor/internal/channels"
	"lng-monitor/internal/config"
	"lng-monitor/internal/database"
	"lng-monitor/internal/models"
)

type Predictor struct {
	params *config.FVMParams
	predCfg *config.PredictionParams
	db     *database.DB
	in     <-chan channels.SensorBatch
	out    chan<- channels.PredictionOutput

	mu sync.Mutex

	tankStates map[int]*tankState
}

type tankState struct {
	layerTemp     []float64
	layerDensity  []float64
	layerHeight   []float64
	previousTemp  []float64
	previousDens  []float64
	effectiveDt   float64
	lastResidual  float64
	tempHistory   [][]float64
	densityHistory [][]float64
}

func NewPredictor(
	fvmParams *config.FVMParams,
	predCfg *config.PredictionParams,
	db *database.DB,
	in <-chan channels.SensorBatch,
	out chan<- channels.PredictionOutput,
) *Predictor {
	n := fvmParams.NumLayers
	p := &Predictor{
		params:     fvmParams,
		predCfg:    predCfg,
		db:         db,
		in:         in,
		out:        out,
		tankStates: make(map[int]*tankState),
	}

	for tankID := 1; tankID <= 4; tankID++ {
		st := &tankState{
			layerTemp:     make([]float64, n),
			layerDensity:  make([]float64, n),
			layerHeight:   make([]float64, n),
			previousTemp:  make([]float64, n),
			previousDens:  make([]float64, n),
			effectiveDt:   float64(predCfg.IntervalSec),
			tempHistory:   make([][]float64, 0),
			densityHistory: make([][]float64, 0),
		}
		layerH := fvmParams.TankHeight / float64(n)
		for i := 0; i < n; i++ {
			st.layerHeight[i] = layerH
			st.layerTemp[i] = -162.0 + float64(i)*0.5
			st.layerDensity[i] = 450.0 + float64(i)*3.0
			st.previousTemp[i] = st.layerTemp[i]
			st.previousDens[i] = st.layerDensity[i]
		}
		p.tankStates[tankID] = st
	}

	return p
}

func (p *Predictor) Start(ctx context.Context) {
	go p.consumeLoop(ctx)

	go p.periodicPredict(ctx)

	log.Printf("rollover_predictor started (num_layers=%d dt=%ds)", p.params.NumLayers, p.predCfg.IntervalSec)
}

func (p *Predictor) consumeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case batch := <-p.in:
			p.ingestBatch(batch)
		}
	}
}

func (p *Predictor) ingestBatch(batch channels.SensorBatch) {
	p.mu.Lock()
	defer p.mu.Unlock()

	st, ok := p.tankStates[batch.TankID]
	if !ok {
		return
	}

	n := p.params.NumLayers
	copy(st.previousTemp, st.layerTemp)
	copy(st.previousDens, st.layerDensity)

	rawTemp := make([]float64, n)
	copy(rawTemp, st.layerTemp)
	rawDens := make([]float64, n)
	copy(rawDens, st.layerDensity)

	layerTempAvg := make(map[int]float64)
	layerTempCount := make(map[int]int)
	for _, t := range batch.Temperatures {
		layerTempAvg[t.LayerIndex] += t.ValueCelsius
		layerTempCount[t.LayerIndex]++
	}
	for k, v := range layerTempAvg {
		idx := k - 1
		if idx >= 0 && idx < n {
			rawTemp[idx] = v / float64(layerTempCount[k])
		}
	}

	layerDensAvg := make(map[int]float64)
	layerDensCount := make(map[int]int)
	for _, d := range batch.Densities {
		layerDensAvg[d.LayerIndex] += d.ValueKgM3
		layerDensCount[d.LayerIndex]++
	}
	for k, v := range layerDensAvg {
		idx := k - 1
		if idx >= 0 && idx < n {
			rawDens[idx] = v / float64(layerDensCount[k])
		}
	}

	for i := 0; i < n; i++ {
		st.layerTemp[i] = st.previousTemp[i] + p.params.AlphaTempRelax*(rawTemp[i]-st.previousTemp[i])
		st.layerDensity[i] = st.previousDens[i] + p.params.AlphaDensityRelax*(rawDens[i]-st.previousDens[i])
	}

	tempSlice := make([]float64, n)
	densSlice := make([]float64, n)
	copy(tempSlice, st.layerTemp)
	copy(densSlice, st.layerDensity)
	st.tempHistory = append(st.tempHistory, tempSlice)
	st.densityHistory = append(st.densityHistory, densSlice)
	if len(st.tempHistory) > p.params.HistoryWindow {
		st.tempHistory = st.tempHistory[1:]
		st.densityHistory = st.densityHistory[1:]
	}
}

func (p *Predictor) periodicPredict(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(p.predCfg.IntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.runAllPredictions(ctx)
		}
	}
}

func (p *Predictor) runAllPredictions(ctx context.Context) {
	tanks, err := p.db.GetTanks(ctx)
	if err != nil {
		log.Printf("predictor: get tanks: %v", err)
		return
	}

	for _, tank := range tanks {
		result := p.predict(tank.ID)

		pred := models.RolloverPrediction{
			Time:               time.Now().UTC(),
			TankID:             tank.ID,
			RiskIndex:          result.RiskIndex,
			LayerStabilityScore: result.Stability,
			PredictedRolloverTime: result.PredictedRollover,
			MaxTempGradient:    result.MaxTempGrad,
			MaxDensityGradient: result.MaxDensGrad,
			ModelVersion:       "fvm-v1.2",
		}
		if err := p.db.InsertPrediction(ctx, pred); err != nil {
			log.Printf("predictor: insert error tank=%d: %v", tank.ID, err)
		}

		select {
		case p.out <- result:
		default:
			log.Printf("predictor: output channel full, dropping tank=%d", tank.ID)
		}
	}
}

func (p *Predictor) predict(tankID int) channels.PredictionOutput {
	p.mu.Lock()
	defer p.mu.Unlock()

	st, ok := p.tankStates[tankID]
	if !ok {
		return channels.PredictionOutput{TankID: tankID, Timestamp: time.Now().UTC()}
	}

	n := p.params.NumLayers
	maxLayerTempDiff := 0.0
	maxLayerDensDiff := 0.0

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			td := math.Abs(st.layerTemp[i] - st.layerTemp[j])
			dd := math.Abs(st.layerDensity[i] - st.layerDensity[j])
			if td > maxLayerTempDiff {
				maxLayerTempDiff = td
			}
			if dd > maxLayerDensDiff {
				maxLayerDensDiff = dd
			}
		}
	}

	raTemp := p.computeRayleighTemp(maxLayerTempDiff, st.layerDensity[n/2])
	raDens := p.computeRayleighDensity(maxLayerDensDiff, st.layerDensity[n/2])
	raRatio := math.Max(raTemp, raDens) / p.params.RACritical

	w := p.predCfg.RiskWeights
	tempContrib := math.Min(maxLayerTempDiff/p.params.Alarm.TempDiffThreshold, 1.0)
	densContrib := math.Min(maxLayerDensDiff/p.params.Alarm.DensityDiffThreshold, 1.0)
	raContrib := math.Min(raRatio, 1.0)
	riskIndex := w.TempContrib*tempContrib + w.DensityContrib*densContrib + w.RAContrib*raContrib
	riskIndex = math.Min(riskIndex, 1.0)

	p.solveHeatDiffusion(st)
	p.solveMassDiffusion(st)

	driftRate := p.computeDriftRate(st)
	var predictedRollover *time.Time
	if riskIndex > p.predCfg.RolloverTriggerRisk && driftRate > 0 {
		hours := (p.predCfg.RolloverTargetRisk - riskIndex) / driftRate
		if hours > 0 {
			t := time.Now().UTC().Add(time.Duration(hours * float64(time.Hour)))
			predictedRollover = &t
		}
	}

	return channels.PredictionOutput{
		Timestamp:        time.Now().UTC(),
		TankID:           tankID,
		RiskIndex:        riskIndex,
		Stability:        1.0 - riskIndex,
		MaxTempGrad:      maxLayerTempDiff,
		MaxDensGrad:      maxLayerDensDiff,
		PredictedRollover: predictedRollover,
	}
}

func (p *Predictor) computeRayleighTemp(deltaT float64, rhoRef float64) float64 {
	if deltaT <= 0 {
		return 0
	}
	alpha := p.params.LNGThermalConduct / (rhoRef * p.params.LNGSpecificHeat)
	return p.params.GravitationalAccel * p.params.LNGThermalExpansion * deltaT *
		math.Pow(p.params.TankHeight, 3) / (p.params.LNGKinematicVisc * alpha)
}

func (p *Predictor) computeRayleighDensity(deltaRho float64, rhoRef float64) float64 {
	if deltaRho <= 0 {
		return 0
	}
	alpha := p.params.LNGThermalConduct / (rhoRef * p.params.LNGSpecificHeat)
	return p.params.GravitationalAccel * deltaRho / rhoRef *
		math.Pow(p.params.TankHeight, 3) / (p.params.LNGKinematicVisc * alpha)
}

func (p *Predictor) computeAdaptiveTimeStep(alpha float64, st *tankState) float64 {
	dx := st.layerHeight[0]
	cflDt := p.params.CFLLimit * dx * dx / alpha
	dt := math.Min(cflDt, p.params.MaxTimeStep)
	dt = math.Max(dt, p.params.MinTimeStep)
	if st.lastResidual > 1e-2 {
		dt = math.Max(p.params.MinTimeStep, dt*0.5)
	} else if st.lastResidual < 1e-6 {
		dt = math.Min(p.params.MaxTimeStep, dt*1.5)
	}
	return dt
}

func (p *Predictor) solveHeatDiffusion(st *tankState) {
	n := p.params.NumLayers
	rho := st.layerDensity[n/2]
	thermalAlpha := p.params.LNGThermalConduct / (rho * p.params.LNGSpecificHeat)
	dt := p.computeAdaptiveTimeStep(thermalAlpha, st)
	st.effectiveDt = dt

	saved := make([]float64, n)
	copy(saved, st.layerTemp)
	prevIter := make([]float64, n)
	copy(prevIter, st.layerTemp)

	for iter := 0; iter < p.params.MaxSubIterations; iter++ {
		rawResult := make([]float64, n)
		copy(rawResult, st.layerTemp)

		for i := 1; i < n-1; i++ {
			dx := st.layerHeight[i]
			d2T := (st.layerTemp[i+1] - 2*st.layerTemp[i] + st.layerTemp[i-1]) / (dx * dx)
			rawResult[i] = st.layerTemp[i] + thermalAlpha*dt*d2T
		}

		dx := st.layerHeight[0]
		d2T := (st.layerTemp[1] - st.layerTemp[0]) / (dx * dx)
		rawResult[0] = st.layerTemp[0] + thermalAlpha*dt*d2T*0.5

		dx = st.layerHeight[n-1]
		d2T = (st.layerTemp[n-2] - st.layerTemp[n-1]) / (dx * dx)
		rawResult[n-1] = st.layerTemp[n-1] + thermalAlpha*dt*d2T*0.5

		for i := 0; i < n; i++ {
			change := rawResult[i] - saved[i]
			if change > p.params.MaxTempChange {
				change = p.params.MaxTempChange
			} else if change < -p.params.MaxTempChange {
				change = -p.params.MaxTempChange
			}
			st.layerTemp[i] = saved[i] + p.params.AlphaTempRelax*change
		}

		residual := 0.0
		for i := 0; i < n; i++ {
			residual += math.Abs(st.layerTemp[i] - prevIter[i])
		}
		residual /= float64(n)
		st.lastResidual = residual

		if residual < p.params.ConvergenceTol {
			break
		}
		copy(prevIter, st.layerTemp)
	}

	for i := 0; i < n; i++ {
		st.layerTemp[i] = math.Max(p.params.TempRange[0], math.Min(p.params.TempRange[1], st.layerTemp[i]))
	}
}

func (p *Predictor) solveMassDiffusion(st *tankState) {
	n := p.params.NumLayers
	dEff := p.params.MassDiffusionCoeff
	dt := p.computeAdaptiveTimeStep(dEff, st)
	subDt := math.Min(dt, st.effectiveDt)

	saved := make([]float64, n)
	copy(saved, st.layerDensity)
	prevIter := make([]float64, n)
	copy(prevIter, st.layerDensity)

	for iter := 0; iter < p.params.MaxSubIterations; iter++ {
		rawResult := make([]float64, n)
		copy(rawResult, st.layerDensity)

		for i := 1; i < n-1; i++ {
			dx := st.layerHeight[i]
			d2Rho := (st.layerDensity[i+1] - 2*st.layerDensity[i] + st.layerDensity[i-1]) / (dx * dx)
			rawResult[i] = st.layerDensity[i] + dEff*subDt*d2Rho
		}

		dx := st.layerHeight[0]
		d2Rho := (st.layerDensity[1] - st.layerDensity[0]) / (dx * dx)
		rawResult[0] = st.layerDensity[0] + dEff*subDt*d2Rho*0.5

		dx = st.layerHeight[n-1]
		d2Rho = (st.layerDensity[n-2] - st.layerDensity[n-1]) / (dx * dx)
		rawResult[n-1] = st.layerDensity[n-1] + dEff*subDt*d2Rho*0.5

		for i := 0; i < n; i++ {
			change := rawResult[i] - saved[i]
			if change > p.params.MaxDensityChange {
				change = p.params.MaxDensityChange
			} else if change < -p.params.MaxDensityChange {
				change = -p.params.MaxDensityChange
			}
			st.layerDensity[i] = saved[i] + p.params.AlphaDensityRelax*change
		}

		residual := 0.0
		for i := 0; i < n; i++ {
			residual += math.Abs(st.layerDensity[i] - prevIter[i])
		}
		residual /= float64(n)

		if residual < p.params.ConvergenceTol {
			break
		}
		copy(prevIter, st.layerDensity)
	}

	for i := 0; i < n; i++ {
		st.layerDensity[i] = math.Max(p.params.DensityRange[0], math.Min(p.params.DensityRange[1], st.layerDensity[i]))
	}
}

func (p *Predictor) computeDriftRate(st *tankState) float64 {
	n := p.params.NumLayers
	if len(st.tempHistory) < p.predCfg.DriftWindow {
		return 0
	}

	histLen := len(st.tempHistory)
	window := p.predCfg.DriftWindow
	if histLen < window {
		window = histLen
	}

	recentRisk := make([]float64, 0, window)
	for i := histLen - window; i < histLen; i++ {
		maxTD := 0.0
		maxDD := 0.0
		for j := 0; j < n; j++ {
			for k := j + 1; k < n; k++ {
				td := math.Abs(st.tempHistory[i][j] - st.tempHistory[i][k])
				dd := math.Abs(st.densityHistory[i][j] - st.densityHistory[i][k])
				if td > maxTD {
					maxTD = td
				}
				if dd > maxDD {
					maxDD = dd
				}
			}
		}
		tc := math.Min(maxTD/p.params.Alarm.TempDiffThreshold, 1.0)
		dc := math.Min(maxDD/p.params.Alarm.DensityDiffThreshold, 1.0)
		recentRisk = append(recentRisk, 0.5*tc+0.5*dc)
	}

	if len(recentRisk) < 2 {
		return 0
	}

	driftSum := 0.0
	for i := 1; i < len(recentRisk); i++ {
		driftSum += recentRisk[i] - recentRisk[i-1]
	}

	return driftSum / float64(len(recentRisk)-1) / float64(p.predCfg.IntervalSec) * 3600
}
