import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
export default defineConfig({plugins:[react()],build:{outDir:'dist',emptyOutDir:true},server:{proxy:{'/api':'http://127.0.0.1:8080','/mcp':'http://127.0.0.1:8080','/v1':'http://127.0.0.1:8080'}},test:{environment:'jsdom',setupFiles:['./src/setup.ts']}})
