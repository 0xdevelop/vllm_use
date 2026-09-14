import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

describe('admin authentication', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
  })

  it('validates before storing the bootstrap token and revealing navigation', async () => {
    const fetcher = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer bootstrap-secret')
      return Promise.resolve(new Response('[]', { headers: { 'Content-Type': 'application/json' } }))
    })
    vi.stubGlobal('fetch', fetcher)

    render(<QueryClientProvider client={new QueryClient()}><App><p>内容</p></App></QueryClientProvider>)
    fireEvent.change(screen.getByLabelText('管理员令牌'), { target: { value: 'bootstrap-secret' } })
    fireEvent.click(screen.getByRole('button', { name: '进入' }))

    expect(sessionStorage.getItem('vllm-use-admin-token')).toBeNull()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
    await screen.findByRole('navigation')
    expect(sessionStorage.getItem('vllm-use-admin-token')).toBe('bootstrap-secret')
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('keeps an invalid token out of session storage and shows the error', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({ error: 'unauthorized' }), { status: 401 }))))

    render(<QueryClientProvider client={new QueryClient()}><App><p>内容</p></App></QueryClientProvider>)
    fireEvent.change(screen.getByLabelText('管理员令牌'), { target: { value: 'wrong-secret' } })
    fireEvent.click(screen.getByRole('button', { name: '进入' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('令牌无效或没有管理权限')
    expect(sessionStorage.getItem('vllm-use-admin-token')).toBeNull()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  })

  it('revalidates a restored session token before showing the console', async () => {
    sessionStorage.setItem('vllm-use-admin-token', 'expired-secret')
    let rejectRequest: ((response: Response) => void) | undefined
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { rejectRequest = resolve })))

    render(<QueryClientProvider client={new QueryClient()}><App><p>内容</p></App></QueryClientProvider>)
    expect(screen.getByText('正在验证已保存的会话…')).toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()

    rejectRequest?.(new Response(JSON.stringify({ error: 'unauthorized' }), { status: 401 }))
    await waitFor(() => expect(sessionStorage.getItem('vllm-use-admin-token')).toBeNull())
    expect(await screen.findByRole('alert')).toHaveTextContent('已保存的会话已失效，请重新输入令牌')
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  })
})
