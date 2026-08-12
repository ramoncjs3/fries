import js from '@eslint/js'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import globals from 'globals'
import tseslint from 'typescript-eslint'

/*
 * 一致性靠三层强制（DECISIONS.md §7.1），这里是**第三层：CI 拦截**。
 *
 * 前两层是「没有第二个选择」（服务端数据只能走 client.ts）和「布局组件封死」
 * （只能用 ListPage / FormDialog / DetailPage）。这一层管的是那些嘴上说了、
 * 但一忙起来就会破例的规矩 —— 交给机器就不会破例。
 */
const hardRules = {
  // 颜色和尺寸只能用 Tailwind token：写死的 hex 在暗色模式下必然翻车
  'no-restricted-syntax': [
    'error',
    {
      selector: "JSXAttribute[name.name='style']",
      message: '不要写行内 style。用 Tailwind class；确实需要动态值就加一个 CSS 变量。',
    },
    {
      selector: 'JSXAttribute[name.name=/^(className|class)$/] Literal[value=/#[0-9a-fA-F]{3,8}\\b/]',
      message: '不要写死颜色。用 src/index.css 里的 token（bg-primary / text-muted-foreground …）。',
    },
    {
      selector: 'JSXAttribute[name.name=/^(className|class)$/] Literal[value=/\\[[0-9.]+px\\]/]',
      message: '不要写死 px。用 Tailwind 的间距刻度（p-4 / h-9 …），页面之间才对得齐。',
    },
    {
      // 字号只有 6 档（DECISIONS.md §7.5）。没这条拦着，一年之内又会长回 12 个字号。
      selector: 'JSXAttribute[name.name=/^(className|class)$/] Literal[value=/text-\\[[0-9.]/]',
      message:
        '不要写死字号。只能用 text-xs/sm/base/lg/xl/2xl 这 6 档（§7.5）；不够用就先改刻度，别就地发明。',
    },
    {
      selector: 'JSXAttribute[name.name=/^(className|class)$/] Literal[value=/\\btext-(3xl|4xl|5xl|6xl|7xl|8xl|9xl)\\b/]',
      message: '刻度里没有这一档。页面标题最大就是 text-2xl（24px，§7.5）。',
    },
    {
      selector: 'MemberExpression[property.name=/^toLocale(Date|Time)?String$/]',
      message:
        '时间一律用 <DateTime> 渲染（固定北京时间，DECISIONS.md §2.5）。裸调 toLocaleString 会跟着浏览器时区跑。',
    },
  ],

  // 请求只能从 client.ts 出去：那里统一处理 cookie、CSRF、错误码、401 跳转
  'no-restricted-globals': [
    'error',
    { name: 'fetch', message: '不要裸调 fetch。用 @/api/client 里的 get / post / put / del。' },
    {
      name: 'confirm',
      message:
        '不要用原生 confirm —— 内嵌浏览器/WebView 会静默屏蔽并返回 false，弹窗就永远关不掉。用 useConfirm + <ConfirmDialog>。',
    },
    { name: 'alert', message: '不要用原生 alert。用 toast 或 <ConfirmDialog>。' },
    { name: 'prompt', message: '不要用原生 prompt。用一个真正的表单弹窗。' },
  ],

  // 弹窗只能用封装好的 FormDialog：滚动、sticky 页脚、脏检查都在里面
  'no-restricted-imports': [
    'error',
    {
      patterns: [
        {
          group: ['@/components/ui/dialog', '**/components/ui/dialog'],
          message: '不要直接用 Dialog。用 @/components/FormDialog —— 滚动和关闭行为都封在里面了。',
        },
      ],
    },
  ],
}

export default tseslint.config(
  { ignores: ['dist', 'node_modules', 'src/api/schema.d.ts'] },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      ...hardRules,
    },
  },
  {
    // client.ts 是唯一允许碰 fetch 的地方 —— 它就是那个出口。
    files: ['src/api/client.ts'],
    rules: { 'no-restricted-globals': 'off' },
  },
  {
    // 规则挡的是「业务页面自己拼弹窗」。`src/components/*Dialog.tsx` 是项目级的
    // 弹窗封装层（FormDialog / ConfirmDialog / SecretDialog），它们就是那层封装本身。
    // 用通配而不是逐个列名字 —— 否则每加一个封装都要回来改一次 lint 配置。
    files: ['src/components/*Dialog.tsx', 'src/components/CommandPalette.tsx', 'src/components/ui/**'],
    rules: { 'no-restricted-imports': 'off' },
  },
  {
    // datetime.ts 里就是要用 Intl 做格式化，规则挡的是「在页面里随手格式化」。
    files: ['src/lib/datetime.ts'],
    rules: { 'no-restricted-syntax': 'off' },
  },
)
