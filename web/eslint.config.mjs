import { defineConfig, globalIgnores } from 'eslint/config'
import nextVitals from 'eslint-config-next/core-web-vitals'

const eslintConfig = defineConfig([
  ...nextVitals,
  // eslint-plugin-react-hooks v7 (bundled by eslint-config-next 16) introduces
  // rules that did not exist in the previous toolchain (react-hooks 5.x).
  // 46 pre-existing call sites across the app violate them; downgraded to warn
  // to keep this dependency migration free of mass code changes.
  {
    rules: {
      'react-hooks/refs': 'warn',
      'react-hooks/set-state-in-effect': 'warn',
      'react-hooks/immutability': 'warn',
    },
  },
  // Migrated warn-level overrides from the former legacy .eslintrc.json.
  // These files carry pre-existing a11y/react findings; downgraded with intent.
  // jsx-a11y rules are provided by eslint-config-next's bundled plugin.
  {
    files: [
      'src/app/admin/page.tsx',
      'src/app/extract/page.tsx',
      'src/app/memory/page.tsx',
      'src/app/projects/page.tsx',
      'src/app/settings/page.tsx',
      'src/components/AppShell.tsx',
      'src/components/graph-view.tsx',
      'src/components/knowledge/sourcepanel.tsx',
      'src/components/ui/card.tsx',
      'src/components/ui/dialog.tsx',
    ],
    rules: {
      'jsx-a11y/click-events-have-key-events': 'warn',
      'jsx-a11y/heading-has-content': 'warn',
      'jsx-a11y/label-has-associated-control': 'warn',
      'jsx-a11y/no-noninteractive-tabindex': 'warn',
      'jsx-a11y/no-static-element-interactions': 'warn',
      'react/no-unescaped-entities': 'warn',
    },
  },
  // Default ignores of eslint-config-next.
  globalIgnores([
    '.next/**',
    'out/**',
    'build/**',
    'coverage/**',
    '**/._*',
    'next-env.d.ts',
  ]),
])

export default eslintConfig
