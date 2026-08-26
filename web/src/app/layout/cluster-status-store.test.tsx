import { act, renderHook, waitFor } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"

import { useClusterStatus } from "@/app/layout/cluster-status-store"
import { healthyManagerOverview } from "@/test/manager-fixtures"

const getOverviewMock = vi.fn()

vi.mock("@/lib/manager-api", () => ({
  getOverview: (...args: unknown[]) => getOverviewMock(...args),
}))

afterEach(() => {
  vi.useRealTimers()
  getOverviewMock.mockReset()
})

test("keeps at most one cluster status request in flight and aborts a stalled request", () => {
  vi.useFakeTimers()
  getOverviewMock.mockReturnValue(new Promise(() => undefined))

  const { unmount } = renderHook(() => useClusterStatus())

  expect(getOverviewMock).toHaveBeenCalledTimes(1)
  const requestInit = getOverviewMock.mock.calls[0]?.[0] as RequestInit

  act(() => vi.advanceTimersByTime(90_000))

  expect(requestInit.signal?.aborted).toBe(true)
  expect(getOverviewMock).toHaveBeenCalledTimes(1)
  unmount()
})

test("projects complete healthy overview evidence into the shared shell status", async () => {
  getOverviewMock.mockResolvedValue(healthyManagerOverview())

  const { result } = renderHook(() => useClusterStatus())

  await waitFor(() => {
    expect(result.current).toMatchObject({ health: "healthy", total: 3, alive: 3, loading: false })
  })
})
