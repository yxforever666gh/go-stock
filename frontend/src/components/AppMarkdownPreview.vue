<script setup>
import {computed} from 'vue'
import {useThemeVars} from 'naive-ui'
import {MdPreview} from 'md-editor-v3'

defineOptions({inheritAttrs: false})

const props = defineProps({
  modelValue: {
    type: String,
    default: '',
  },
  theme: {
    type: String,
    default: '',
  },
})

const themeVars = useThemeVars()

function isDarkColor(value) {
  const color = String(value || '').trim()
  const shortHex = color.match(/^#([0-9a-f]{3})$/i)
  const longHex = color.match(/^#([0-9a-f]{6})$/i)
  const rgb = color.match(/^rgba?\(\s*(\d+)\D+(\d+)\D+(\d+)/i)
  let channels
  if (shortHex) channels = [...shortHex[1]].map(part => parseInt(part + part, 16))
  if (longHex) channels = [0, 2, 4].map(index => parseInt(longHex[1].slice(index, index + 2), 16))
  if (rgb) channels = rgb.slice(1, 4).map(Number)
  if (!channels) return false
  return channels[0] * 0.299 + channels[1] * 0.587 + channels[2] * 0.114 < 128
}

const resolvedTheme = computed(() => {
  if (props.theme === 'dark' || props.theme === 'light') return props.theme
  return isDarkColor(themeVars.value.bodyColor || themeVars.value.baseColor) ? 'dark' : 'light'
})
</script>

<template>
  <MdPreview
      class="app-markdown-surface app-markdown-preview"
      :model-value="modelValue"
      :theme="resolvedTheme"
      v-bind="$attrs"
  />
</template>

<style>
.app-markdown-surface {
  --app-markdown-border: #e5e7eb;
  --app-markdown-muted: #6b7280;
  --app-markdown-soft-bg: #f7f7f8;
  --app-markdown-table-head: #f4f4f5;
  --app-markdown-table-stripe: #fafafa;
  min-width: 0;
  text-align: left;
}

.app-markdown-surface.md-editor-dark {
  --app-markdown-border: #3f3f46;
  --app-markdown-muted: #a1a1aa;
  --app-markdown-soft-bg: #27272a;
  --app-markdown-table-head: #27272a;
  --app-markdown-table-stripe: #202023;
}

.app-markdown-surface .md-editor-preview-wrapper {
  min-width: 0;
  overflow-x: hidden;
}

.app-markdown-surface .md-editor-preview {
  box-sizing: border-box;
  width: min(100%, 860px);
  max-width: 860px;
  min-width: 0;
  margin: 0 auto;
  padding: 32px clamp(18px, 4vw, 48px) 48px;
  font-family: ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
  font-size: 16px;
  font-weight: 400;
  line-height: 1.75;
  overflow-wrap: anywhere;
  text-align: left !important;
}

.app-markdown-editor .md-editor-preview {
  padding-top: 24px;
  padding-bottom: 32px;
}

.app-markdown-surface .md-editor-preview h1,
.app-markdown-surface .md-editor-preview h2,
.app-markdown-surface .md-editor-preview h3,
.app-markdown-surface .md-editor-preview h4,
.app-markdown-surface .md-editor-preview h5,
.app-markdown-surface .md-editor-preview h6,
.app-markdown-surface .md-editor-preview p,
.app-markdown-surface .md-editor-preview li,
.app-markdown-surface .md-editor-preview blockquote,
.app-markdown-surface .md-editor-preview pre,
.app-markdown-surface .md-editor-preview th,
.app-markdown-surface .md-editor-preview td {
  text-align: left !important;
}

.app-markdown-surface .md-editor-preview h1,
.app-markdown-surface .md-editor-preview h2,
.app-markdown-surface .md-editor-preview h3,
.app-markdown-surface .md-editor-preview h4,
.app-markdown-surface .md-editor-preview h5,
.app-markdown-surface .md-editor-preview h6 {
  padding: 0;
  border: 0;
  line-height: 1.3;
  letter-spacing: -0.015em;
}

.app-markdown-surface .md-editor-preview h1 {
  margin: 0 0 1em;
  font-size: 1.85em;
}

.app-markdown-surface .md-editor-preview h2 {
  margin: 1.8em 0 0.75em;
  font-size: 1.45em;
}

.app-markdown-surface .md-editor-preview h3 {
  margin: 1.55em 0 0.65em;
  font-size: 1.2em;
}

.app-markdown-surface .md-editor-preview h4,
.app-markdown-surface .md-editor-preview h5,
.app-markdown-surface .md-editor-preview h6 {
  margin: 1.35em 0 0.55em;
  font-size: 1em;
}

.app-markdown-surface .md-editor-preview p {
  margin: 0 0 1.05em;
}

.app-markdown-surface .md-editor-preview ul,
.app-markdown-surface .md-editor-preview ol {
  margin: 0 0 1.1em;
  padding-left: 1.65em;
}

.app-markdown-surface .md-editor-preview li + li {
  margin-top: 0.35em;
}

.app-markdown-surface .md-editor-preview blockquote {
  margin: 1.2em 0;
  padding: 0.8em 1em;
  color: var(--app-markdown-muted);
  background: var(--app-markdown-soft-bg);
  border-left: 3px solid var(--app-markdown-border);
  border-radius: 0 8px 8px 0;
}

.app-markdown-surface .md-editor-preview blockquote > :last-child {
  margin-bottom: 0;
}

.app-markdown-surface .md-editor-preview .md-editor-code,
.app-markdown-surface .md-editor-preview pre {
  margin: 1.2em 0;
}

.app-markdown-surface .md-editor-preview table {
  display: block;
  width: max-content;
  max-width: 100%;
  margin: 1.35em 0;
  overflow-x: auto;
  border: 1px solid var(--app-markdown-border);
  border-spacing: 0;
  border-collapse: collapse;
  border-radius: 8px;
}

.app-markdown-surface .md-editor-preview thead {
  background: var(--app-markdown-table-head);
}

.app-markdown-surface .md-editor-preview th,
.app-markdown-surface .md-editor-preview td {
  min-width: 8em;
  padding: 10px 14px;
  line-height: 1.55;
  vertical-align: top;
  border: 0;
  border-right: 1px solid var(--app-markdown-border);
  border-bottom: 1px solid var(--app-markdown-border);
}

.app-markdown-surface .md-editor-preview th {
  font-weight: 600;
  white-space: nowrap;
}

.app-markdown-surface .md-editor-preview tr:nth-child(2n) {
  background: var(--app-markdown-table-stripe);
}

.app-markdown-surface .md-editor-preview tr:last-child td {
  border-bottom: 0;
}

.app-markdown-surface .md-editor-preview th:last-child,
.app-markdown-surface .md-editor-preview td:last-child {
  border-right: 0;
}

.app-markdown-surface .md-editor-preview hr {
  margin: 2em 0;
  border: 0;
  border-top: 1px solid var(--app-markdown-border);
}

@media (max-width: 720px) {
  .app-markdown-surface .md-editor-preview {
    padding: 24px 16px 36px;
    font-size: 15px;
  }

  .app-markdown-surface .md-editor-preview h1 {
    font-size: 1.6em;
  }

  .app-markdown-surface .md-editor-preview h2 {
    font-size: 1.35em;
  }
}
</style>
