import globals from 'globals';
import reactHooks from 'eslint-plugin-react-hooks';
import tseslint from 'typescript-eslint';

// 只保留正确性规则：格式交给 Prettier，避免 lint 与格式化互相拉扯。
// no-unused-vars 保持 warn（沿用工程基线），no-explicit-any 收紧为 error。
export default tseslint.config(
  { ignores: ['node_modules', '../internal/server/web-dist'] },
  ...tseslint.configs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
    },
    plugins: { 'react-hooks': reactHooks },
    rules: {
      ...reactHooks.configs.recommended.rules,
      '@typescript-eslint/no-unused-vars': [
        'warn',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      '@typescript-eslint/no-explicit-any': 'error',
    },
  },
  {
    files: ['scripts/**/*.mjs', '*.config.{js,mjs,ts}', 'vite.config.ts'],
    languageOptions: { globals: globals.node },
  },
);
