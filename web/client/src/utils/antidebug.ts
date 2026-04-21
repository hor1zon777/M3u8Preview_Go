// utils/antidebug.ts
// 软反调试：检测 DevTools 打开 / 单步调试，设置 dirty 标志位但不主动破坏用户体验。
// 加密路径（utils/crypto.ts）在加密前查 isDebugging()，命中则抛错中断当前请求。
//
// 启用前提：只在生产构建（import.meta.env.PROD）启用。开发模式不检测，避免误伤调试。
//
// 检测手段（3 个互补）：
//   1. DevTools 尺寸差：window.outerWidth/Height - innerWidth/Height 在 DevTools 停靠时显著增大
//   2. console.log 的 getter 陷阱：DevTools 打开时会读取对象 toString，触发陷阱
//   3. 定时器漂移：单步调试时 setInterval 回调间隔显著变大（>2x 期望值）
//
// 历史注记：曾经有第 4 个检测点——debugger 语句执行耗时。但 Vite/esbuild 生产
// minify 默认会 drop debugger 语句（即便 simplify:false 也拦不住），切 terser
// 会拖慢构建 10x，权衡后取舍。若只开独立窗口的 DevTools 可绕开 outerWidth 检测，
// 此时主要靠 console getter 陷阱和定时器漂移兜底。
//
// 严格 CSP 兼容：不使用 new Function / eval / unsafe-eval。
//
// 失败副作用：仅设置模块级 dirty 标志，不 reload / 不清 token / 不破坏 UI。

let dirty = false;
let started = false;

/** 当前是否疑似处于调试环境。加密前查它，命中就抛错。 */
export function isDebugging(): boolean {
  return dirty;
}

/** 测试/重置用。生产代码不应调用。 */
export function _resetAntiDebugForTest(): void {
  dirty = false;
  started = false;
}

/**
 * 启动反调试循环。幂等（重复调用只启动一次）。
 * 只在生产环境启用；dev 下直接返回。
 */
export function startAntiDebug(): void {
  if (started) return;
  started = true;
  if (typeof window === 'undefined') return;

  // 初次立即跑一次
  checkSize();
  installConsoleTrap();

  // 周期性复检尺寸差
  setInterval(() => {
    if (dirty) return; // 已命中则停止消耗 CPU
    checkSize();
  }, 2500);

  // 定时器漂移检测
  scheduleDriftCheck();
}

function checkSize(): void {
  // DevTools 停靠右/下时会造成 outer - inner 显著差距。
  // 阈值 160 比较宽容；独立窗口模式检测不到是可接受的取舍。
  const diffW = window.outerWidth - window.innerWidth;
  const diffH = window.outerHeight - window.innerHeight;
  if (diffW > 160 || diffH > 160) {
    dirty = true;
  }
}

function installConsoleTrap(): void {
  // 构造一个 getter 被访问时触发的对象。
  // DevTools 打开时，控制台渲染对象会读 toString/Symbol.toPrimitive，触发陷阱。
  const trap: Record<string, unknown> = {};
  Object.defineProperty(trap, 'toString', {
    get() {
      dirty = true;
      return () => '';
    },
  });
  // 先打一次，后续如果 DevTools 打开在重放 log 时触发
  try {
    console.debug(trap);
  } catch {
    /* 某些浏览器禁用 console 会抛错，忽略 */
  }
}

function scheduleDriftCheck(): void {
  // 高分辨率心跳：若实际间隔显著大于期望，说明被单步调试拖住
  const expected = 1000;
  let last = performance.now();
  setInterval(() => {
    const now = performance.now();
    const drift = now - last - expected;
    if (drift > 500) {
      dirty = true;
    }
    last = now;
  }, expected);
}
