document.addEventListener('DOMContentLoaded', () => {
    App.init().then(() => {
        console.log('LNG储罐翻滚预测与安全监控系统 已启动');
    }).catch(err => {
        console.error('系统初始化失败:', err);
    });
});
