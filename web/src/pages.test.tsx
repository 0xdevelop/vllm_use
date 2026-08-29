import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { pages } from './pages'

const response = (body: unknown) => Promise.resolve(new Response(JSON.stringify(body), {
  status: 200,
  headers: { 'Content-Type': 'application/json' },
}))

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('request audit table', () => {
  it('renders duplicate client request IDs as distinct audit events without React key collisions', async () => {
    const duplicateRequests = [
      { audit_id: 'audit-2', request_id: 'client-retry', method: 'POST', path: '/v1/responses', model: 'demo', key_id: 'key', remote_addr: '127.0.0.1', status_code: 502, duration_ms: 13, created_at: '2026-08-29T00:00:01Z' },
      { audit_id: 'audit-1', request_id: 'client-retry', method: 'POST', path: '/v1/responses', model: 'demo', key_id: 'key', remote_addr: '127.0.0.1', status_code: 200, duration_ms: 12, created_at: '2026-08-29T00:00:00Z' },
    ]
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const path = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
      if (path === '/api/dashboard') return response({ models: 0, runtime: { status: 'stopped', pid: 0, logs: [] }, downloads: [], recent_requests: duplicateRequests })
      if (path === '/api/gpus') return response([])
      if (path === '/api/mcp') return response({ protocol_version: '2026-07-28', transport: 'streamable-http', stateless: true, recent_requests: [] })
      throw new Error(`unexpected fetch: ${path}`)
    })
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    vi.stubGlobal('fetch', fetchMock)

    const DashboardPage = pages.find(page => page.path === '/dashboard')!.component
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><DashboardPage /></QueryClientProvider>)

    expect(await screen.findAllByText('/v1/responses')).toHaveLength(2)
    expect(consoleError.mock.calls.flat().join(' ')).not.toContain('same key')
    consoleError.mockRestore()
  })
})

describe('settings page', () => {
  it('deletes a persisted setting through the management API', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
      if (path === '/api/settings/theme' && init?.method === 'DELETE') return response({ deleted: true })
      if (path === '/api/settings') return response([{ key: 'theme', value: 'dark', updated_at: '2026-08-29T00:00:00Z' }])
      if (path === '/api/system') return response({ go_version: 'go1.25', goos: 'linux', goarch: 'amd64', cpus: 4 })
      throw new Error(`unexpected fetch: ${path}`)
    })
    vi.stubGlobal('fetch', fetchMock)

    const SettingsPage = pages.find(page => page.path === '/settings')!.component
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><SettingsPage /></QueryClientProvider>)

    expect(await screen.findByDisplayValue('theme')).toHaveAttribute('readonly')
    fireEvent.click(screen.getByRole('button', { name: '删除' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/settings/theme', expect.objectContaining({ method: 'DELETE' })))
    await waitFor(() => expect(screen.queryByDisplayValue('theme')).not.toBeInTheDocument())
  })
})
