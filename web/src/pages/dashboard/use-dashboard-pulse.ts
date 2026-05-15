import { useMemo } from 'react'

export type PulseSeries = {
  latest: number
  peak: number
  avg: number
  series: number[]
}

export type PulseData = {
  sendPerSec: PulseSeries
  deliverPerSec: PulseSeries
  connections: PulseSeries
  sendLatencyP99: PulseSeries
  deliveryLatencyP99: PulseSeries
  sendFailRate: PulseSeries
  deliveryFailRate: PulseSeries
  activeChannels: PulseSeries
  retryQueueDepth: PulseSeries
  fanOutRate: PulseSeries
}

function djb2(str: string): number {
  let hash = 5381
  for (let i = 0; i < str.length; i++) {
    hash = ((hash << 5) + hash) ^ str.charCodeAt(i)
    hash = hash >>> 0
  }
  return hash
}

function makePrng(seed: number) {
  let s = seed >>> 0
  return function rand(): number {
    s = (s * 1664525 + 1013904223) >>> 0
    return s / 4294967296
  }
}

function buildSeries(rand: () => number, base: number, variance: number): PulseSeries {
  const series: number[] = []
  let current = base
  for (let i = 0; i < 60; i++) {
    current = Math.max(0, current + (rand() - 0.5) * variance)
    series.push(Math.round(current))
  }
  const latest = series[59]
  const peak = Math.max(...series)
  const avg = Math.round(series.reduce((a, b) => a + b, 0) / 60)
  return { latest, peak, avg, series }
}

export function generatePulseData(seed: string): PulseData {
  const hash = djb2(seed)
  const rand = makePrng(hash)

  return {
    sendPerSec: buildSeries(rand, 1200, 400),
    deliverPerSec: buildSeries(rand, 1100, 380),
    connections: buildSeries(rand, 850, 100),
    sendLatencyP99: buildSeries(rand, 45, 20),
    deliveryLatencyP99: buildSeries(rand, 120, 50),
    sendFailRate: buildSeries(rand, 1, 2),
    deliveryFailRate: buildSeries(rand, 1, 2),
    activeChannels: buildSeries(rand, 320, 80),
    retryQueueDepth: buildSeries(rand, 5, 8),
    fanOutRate: buildSeries(rand, 12, 6),
  }
}

export function useDashboardPulse(generatedAt: string | null): PulseData | null {
  return useMemo(() => {
    if (generatedAt === null) return null
    return generatePulseData(generatedAt)
  }, [generatedAt])
}
