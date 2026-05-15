import { describe, it, expect } from 'vitest'
import { generatePulseData } from './use-dashboard-pulse'

describe('generatePulseData', () => {
  it('returns 60 data points for each series', () => {
    const data = generatePulseData('test-seed')
    expect(data.sendPerSec.series).toHaveLength(60)
    expect(data.deliverPerSec.series).toHaveLength(60)
    expect(data.connections.series).toHaveLength(60)
    expect(data.sendLatencyP99.series).toHaveLength(60)
    expect(data.deliveryLatencyP99.series).toHaveLength(60)
    expect(data.sendFailRate.series).toHaveLength(60)
    expect(data.deliveryFailRate.series).toHaveLength(60)
    expect(data.activeChannels.series).toHaveLength(60)
    expect(data.retryQueueDepth.series).toHaveLength(60)
    expect(data.fanOutRate.series).toHaveLength(60)
  })

  it('is deterministic for the same seed', () => {
    const a = generatePulseData('same-seed')
    const b = generatePulseData('same-seed')
    expect(a).toEqual(b)
  })

  it('produces different data for different seeds', () => {
    const a = generatePulseData('seed-alpha')
    const b = generatePulseData('seed-beta')
    expect(a.sendPerSec.series).not.toEqual(b.sendPerSec.series)
  })

  it('latest equals the last element of series', () => {
    const data = generatePulseData('latest-check')
    expect(data.sendPerSec.latest).toBe(data.sendPerSec.series[59])
    expect(data.deliverPerSec.latest).toBe(data.deliverPerSec.series[59])
    expect(data.connections.latest).toBe(data.connections.series[59])
    expect(data.deliveryLatencyP99.latest).toBe(data.deliveryLatencyP99.series[59])
  })

  it('peak is the max of the series', () => {
    const data = generatePulseData('peak-check')
    expect(data.sendPerSec.peak).toBe(Math.max(...data.sendPerSec.series))
    expect(data.connections.peak).toBe(Math.max(...data.connections.series))
    expect(data.deliveryFailRate.peak).toBe(Math.max(...data.deliveryFailRate.series))
    expect(data.fanOutRate.peak).toBe(Math.max(...data.fanOutRate.series))
  })

  it('avg is the mean of the series (rounded)', () => {
    const data = generatePulseData('avg-check')
    const mean = (series: number[]) => Math.round(series.reduce((a, b) => a + b, 0) / series.length)
    expect(data.sendPerSec.avg).toBe(mean(data.sendPerSec.series))
    expect(data.activeChannels.avg).toBe(mean(data.activeChannels.series))
    expect(data.retryQueueDepth.avg).toBe(mean(data.retryQueueDepth.series))
  })
})
