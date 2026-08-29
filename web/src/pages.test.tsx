import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { pages } from './pages'

const response = (body: unknown) => Promise.resolve(new Response(JSON.stringify(body), {
  status: 200,
  headers: { 'Content-Type': 'application/json' },
}))

afterEach(() => vi.unstubAllGlobals())

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
