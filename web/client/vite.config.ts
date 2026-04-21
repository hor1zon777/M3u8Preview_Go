import { defineConfig, loadEnv, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import JavaScriptObfuscator from 'javascript-obfuscator';

/**
 * securityObfuscator —— 对登录加密协议相关源文件做强混淆。
 *
 * 覆盖范围：src/utils/crypto.ts + src/utils/fingerprint.ts + src/services/authApi.ts。
 * 只在 mode=production 启用，避免拖慢 dev 和破坏 source map 调试体验。
 *
 * 采用 Medium 预设 + 关闭 debugProtection（避免生产用户误进 DevTools 时页面卡死）。
 * enforce:'post' 让 esbuild/typescript 先转译再混淆，保证 ESM 输出格式不被破坏。
 */
function securityObfuscator(): Plugin {
  const patterns = [
    /[\\/]src[\\/]utils[\\/]crypto\.ts$/,
    /[\\/]src[\\/]utils[\\/]fingerprint\.ts$/,
    /[\\/]src[\\/]services[\\/]authApi\.ts$/,
  ];
  return {
    name: 'm3u8preview-security-obfuscator',
    enforce: 'post',
    apply: 'build',
    transform(code, id) {
      if (!patterns.some((p) => p.test(id))) return null;
      const result = JavaScriptObfuscator.obfuscate(code, {
        compact: true,
        controlFlowFlattening: true,
        controlFlowFlatteningThreshold: 0.75,
        deadCodeInjection: true,
        deadCodeInjectionThreshold: 0.4,
        debugProtection: false,
        disableConsoleOutput: false,
        identifierNamesGenerator: 'hexadecimal',
        log: false,
        numbersToExpressions: true,
        renameGlobals: false,
        selfDefending: true,
        simplify: true,
        splitStrings: true,
        splitStringsChunkLength: 10,
        stringArray: true,
        stringArrayCallsTransform: true,
        stringArrayCallsTransformThreshold: 0.75,
        stringArrayEncoding: ['base64'],
        stringArrayIndexShift: true,
        stringArrayRotate: true,
        stringArrayShuffle: true,
        stringArrayWrappersCount: 2,
        stringArrayWrappersChainedCalls: true,
        stringArrayWrappersParametersMaxCount: 4,
        stringArrayWrappersType: 'function',
        stringArrayThreshold: 0.75,
        transformObjectKeys: true,
        unicodeEscapeSequence: false,
        sourceMap: false,
      });
      return { code: result.getObfuscatedCode(), map: null };
    },
  };
}

export default defineConfig(({ mode }) => {
  // 从 web/ 根目录 + 仓库根目录加载 .env（后者覆盖）
  const webEnv = loadEnv(mode, path.resolve(__dirname, '..'), '');
  const repoEnv = loadEnv(mode, path.resolve(__dirname, '../..'), '');
  const apiPort = repoEnv.PORT || webEnv.PORT || '3000';
  const apiTarget = `http://localhost:${apiPort}`;

  return {
    plugins: [react(), securityObfuscator()],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
      },
    },
    server: {
      host: '0.0.0.0',
      port: 5173,
      proxy: {
        '/api': {
          target: apiTarget,
          changeOrigin: true,
        },
        '/uploads': {
          target: apiTarget,
          changeOrigin: true,
        },
      },
    },
  };
});
