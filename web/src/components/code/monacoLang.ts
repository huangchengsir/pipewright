/**
 * monacoLang — Story 7-4: 文件名/扩展名 → Monaco language id 映射。
 *
 * 纯函数,不引入 monaco(保持主 bundle 干净)。返回 Monaco 内置 language id;
 * 未知扩展返回 'plaintext'(Monaco 仍可只读显示)。
 */

// 特殊文件名(无扩展或约定俗成)。
const BY_NAME: Record<string, string> = {
  dockerfile: 'dockerfile',
  makefile: 'plaintext',
  '.gitignore': 'plaintext',
  '.dockerignore': 'plaintext',
  '.env': 'plaintext',
  'go.mod': 'plaintext',
  'go.sum': 'plaintext',
}

// 扩展名 → Monaco language id。
const BY_EXT: Record<string, string> = {
  ts: 'typescript',
  tsx: 'typescript',
  js: 'javascript',
  jsx: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  vue: 'html', // Monaco 无原生 vue;html 高亮 template 区可接受
  json: 'json',
  jsonc: 'json',
  go: 'go',
  py: 'python',
  rb: 'ruby',
  rs: 'rust',
  java: 'java',
  kt: 'kotlin',
  kts: 'kotlin',
  c: 'c',
  h: 'c',
  cpp: 'cpp',
  cc: 'cpp',
  cxx: 'cpp',
  hpp: 'cpp',
  cs: 'csharp',
  php: 'php',
  swift: 'swift',
  scala: 'scala',
  sh: 'shell',
  bash: 'shell',
  zsh: 'shell',
  sql: 'sql',
  html: 'html',
  htm: 'html',
  xml: 'xml',
  svg: 'xml',
  css: 'css',
  scss: 'scss',
  less: 'less',
  md: 'markdown',
  markdown: 'markdown',
  yaml: 'yaml',
  yml: 'yaml',
  toml: 'ini',
  ini: 'ini',
  conf: 'ini',
  dockerfile: 'dockerfile',
  txt: 'plaintext',
}

/** 由文件路径推断 Monaco language id。 */
export function languageForPath(filePath: string): string {
  const name = (filePath.split('/').pop() ?? '').toLowerCase()
  if (name in BY_NAME) return BY_NAME[name]
  const dot = name.lastIndexOf('.')
  if (dot > 0) {
    const ext = name.slice(dot + 1)
    if (ext in BY_EXT) return BY_EXT[ext]
  }
  // 形如 Dockerfile.dev
  if (name.startsWith('dockerfile')) return 'dockerfile'
  return 'plaintext'
}
