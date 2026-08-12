import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import App from './App'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

describe('admin authentication', () => {
  it('stores the bootstrap token and reveals navigation', async () => {
    render(<QueryClientProvider client={new QueryClient()}><App><p>内容</p></App></QueryClientProvider>)
    await userEvent.type(screen.getByLabelText('管理员令牌'), 'bootstrap-secret')
    await userEvent.click(screen.getByRole('button', { name: '进入' }))
    expect(screen.getByRole('navigation')).toBeInTheDocument()
    expect(sessionStorage.getItem('vllm-use-admin-token')).toBe('bootstrap-secret')
  })
})
