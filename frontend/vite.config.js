import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'
import AutoImport from 'unplugin-auto-import/vite';
import Components from 'unplugin-vue-components/vite';
import { TDesignResolver } from '@tdesign-vue-next/auto-import-resolver';

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [
      vue(),
      AutoImport({
          resolvers: [TDesignResolver({
              library: 'chat'
          })],
      }),
      Components({
          resolvers: [TDesignResolver({
              library: 'chat'
          })],
      }),
  ],
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
          '/healthz': {
              target: 'http://127.0.0.1:34115',
              changeOrigin: true
          }
      }
  }
})
