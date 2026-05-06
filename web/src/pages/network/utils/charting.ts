import type { IntlShape } from "react-intl"

import type { ChartConfig } from "@/components/ui/chart"

export const networkChartColors = {
  alive: "hsl(142 70% 45%)",
  suspect: "hsl(38 92% 50%)",
  dead: "hsl(0 72% 51%)",
  draining: "hsl(217 91% 60%)",
  active: "hsl(199 89% 48%)",
  idle: "hsl(210 14% 70%)",
  tx: "hsl(24 95% 53%)",
  rx: "hsl(173 80% 40%)",
  success: "hsl(142 70% 45%)",
  failures: "hsl(0 72% 51%)",
  expected: "hsl(217 91% 60%)",
  dial: "hsl(18 92% 48%)",
  queue: "hsl(38 92% 50%)",
  timeout: "hsl(0 72% 51%)",
  remote: "hsl(330 81% 60%)",
  p50: "hsl(173 80% 40%)",
  p95: "hsl(24 95% 53%)",
  p99: "hsl(0 72% 51%)",
}

export function networkChartConfig(intl: IntlShape): ChartConfig {
  return {
    alive: { label: intl.formatMessage({ id: "network.legend.alive" }), color: networkChartColors.alive },
    suspect: { label: intl.formatMessage({ id: "network.legend.suspect" }), color: networkChartColors.suspect },
    dead: { label: intl.formatMessage({ id: "network.legend.dead" }), color: networkChartColors.dead },
    draining: { label: intl.formatMessage({ id: "network.legend.draining" }), color: networkChartColors.draining },
    active: { label: intl.formatMessage({ id: "network.legend.active" }), color: networkChartColors.active },
    idle: { label: intl.formatMessage({ id: "network.legend.idle" }), color: networkChartColors.idle },
    tx: { label: intl.formatMessage({ id: "network.legend.tx" }), color: networkChartColors.tx },
    rx: { label: intl.formatMessage({ id: "network.legend.rx" }), color: networkChartColors.rx },
    success: { label: intl.formatMessage({ id: "network.legend.success" }), color: networkChartColors.success },
    failures: { label: intl.formatMessage({ id: "network.rpc.abnormalFailures" }), color: networkChartColors.failures },
    expected: { label: intl.formatMessage({ id: "network.rpc.expectedLongPollExpiries" }), color: networkChartColors.expected },
    dial: { label: intl.formatMessage({ id: "network.legend.dial" }), color: networkChartColors.dial },
    queue: { label: intl.formatMessage({ id: "network.legend.queue" }), color: networkChartColors.queue },
    timeout: { label: intl.formatMessage({ id: "network.legend.timeout" }), color: networkChartColors.timeout },
    remote: { label: intl.formatMessage({ id: "network.legend.remote" }), color: networkChartColors.remote },
    p50: { label: "P50", color: networkChartColors.p50 },
    p95: { label: "P95", color: networkChartColors.p95 },
    p99: { label: "P99", color: networkChartColors.p99 },
  }
}
