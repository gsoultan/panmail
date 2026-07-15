import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules')) {
            if (id.includes('@mantine')) {
              return 'mantine';
            }
            if (id.includes('@tabler/icons-react')) {
              return 'icons';
            }
            if (id.includes('recharts') || id.includes('d3')) {
              return 'charts';
            }
            if (id.includes('react') || id.includes('@tanstack') || id.includes('zustand')) {
              return 'vendor';
            }
            return 'others';
          }
        }
      }
    },
    chunkSizeWarningLimit: 600, // Optional: slightly increase warning limit if needed, but aim for < 500kb
  }
})
