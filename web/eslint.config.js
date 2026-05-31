// ESLint flat config (ESLint v9) — Vue 3 + TypeScript。
// 取代旧 .eslintrc(v9 起默认 flat config)。组合 @eslint/js + typescript-eslint + eslint-plugin-vue,
// .vue 的 <script lang="ts"> 用 TS 解析器。噪声规则放宽为 warn/off,先让存量代码 lint 通过,
// 保留真正有价值的检查;warning 不阻断 CI(`eslint .` 仅 error 非零退出)。
import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import pluginVue from 'eslint-plugin-vue'
import globals from 'globals'

export default tseslint.config(
  {
    ignores: ['dist/**', 'node_modules/**', 'coverage/**'],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...pluginVue.configs['flat/essential'],
  {
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: { parser: tseslint.parser },
    },
  },
  {
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: { ...globals.browser, ...globals.node },
    },
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-unused-vars': [
        'warn',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrors: 'none' },
      ],
      '@typescript-eslint/no-empty-object-type': 'off',
      'vue/multi-word-component-names': 'off',
      'vue/require-default-prop': 'off',
      'no-empty': ['warn', { allowEmptyCatch: true }],
    },
  },
  {
    files: ['**/*.test.ts', 'e2e/**', 'e2e-real/**', '*.config.{ts,js,mjs}'],
    languageOptions: { globals: { ...globals.node } },
  },
)
