import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'

function packageNameFromId(id) {
    const marker = '/node_modules/'
    const index = id.lastIndexOf(marker)
    if (index < 0) {
        return ''
    }
    const rest = id.slice(index + marker.length)
    const segments = rest.split('/')
    if (segments[0].startsWith('@')) {
        return `${segments[0]}/${segments[1] || ''}`
    }
    return segments[0] || ''
}

function packageRelativePath(id, pkgName) {
    const marker = `/node_modules/${pkgName}/`
    const index = id.lastIndexOf(marker)
    if (index < 0) {
        return ''
    }
    return id.slice(index + marker.length)
}

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [
      vue(),
  ],
  build: {
      chunkSizeWarningLimit: 1600,
      rollupOptions: {
          output: {
              manualChunks(id) {
                  if (!id.includes('node_modules')) {
                      return
                  }
                  const pkgName = packageNameFromId(id)
                  if (!pkgName) {
                      return
                  }
                  const relativePath = packageRelativePath(id, pkgName)

                  if (pkgName === 'vue' || pkgName === 'vue-router') {
                      return 'vendor-vue'
                  }
                  if (pkgName === 'naive-ui' || pkgName.startsWith('@vicons/')) {
                      return 'vendor-ui-naive'
                  }
                  if (pkgName === 'echarts' || pkgName === 'zrender') {
                      return 'vendor-chart'
                  }
                  if (
                      pkgName === 'html2canvas' ||
                      pkgName === 'html-docx-js-typescript' ||
                      pkgName === 'jszip'
                  ) {
                      return 'vendor-export'
                  }
                  if (
                      pkgName === '@tdesign-vue-next/chat' ||
                      pkgName === 'md-editor-v3' ||
                      pkgName === 'markdown-it' ||
                      pkgName.startsWith('markdown-it-') ||
                      pkgName === 'entities' ||
                      pkgName === 'linkify-it' ||
                      pkgName === 'mdurl'
                  ) {
                      return 'vendor-md-core'
                  }
                  if (
                      pkgName === 'codemirror' ||
                      pkgName.startsWith('@codemirror/') ||
                      pkgName.startsWith('@lezer/')
                  ) {
                      return 'vendor-md-editor'
                  }
                  if (pkgName === 'highlight.js') {
                      if (relativePath.includes('/languages/')) {
                          return 'vendor-md-highlight-languages'
                      }
                      return 'vendor-md-core'
                  }
                  if (pkgName === '@vavt/v3-extension') {
                      if (
                          relativePath.includes('html3pdf') ||
                          relativePath.includes('html2canvas') ||
                          relativePath.includes('purify')
                      ) {
                          return 'vendor-md-export'
                      }
                      return 'vendor-md-extension'
                  }
              },
          },
      },
  },
  server: {
      host: '127.0.0.1',
      port: 5173,
      strictPort: true,
      proxy: {
          '/api': {
              target: 'http://127.0.0.1:34115',
              changeOrigin: true,
              ws: true
          },
          '/livez': {
              target: 'http://127.0.0.1:34115',
              changeOrigin: true
          },
          '/readyz': {
              target: 'http://127.0.0.1:34115',
              changeOrigin: true
          }
      }
  }
})
