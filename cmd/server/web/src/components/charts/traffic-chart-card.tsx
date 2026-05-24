import { Area, AreaChart, CartesianGrid, XAxis } from "recharts"

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart"
import type { TrafficChartPoint } from "@/lib/api"
import { formatBytes } from "@/lib/format"

const chartConfig = {
  inboundBytes: {
    label: "Inbound",
    color: "var(--chart-2)",
  },
  outboundBytes: {
    label: "Outbound",
    color: "var(--chart-5)",
  },
} satisfies ChartConfig

export function TrafficChartCard({ data }: { data: TrafficChartPoint[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Inbound and outbound traffic</CardTitle>
        <CardDescription>
          Bytes transferred through the tunnel over the last 7 days.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ChartContainer config={chartConfig}>
          <AreaChart
            accessibilityLayer
            data={data}
            margin={{
              left: 12,
              right: 12,
            }}
          >
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey="bucket"
              tickLine={false}
              axisLine={false}
              tickMargin={8}
              tickFormatter={(value) => value.slice(5)}
            />
            <ChartTooltip
              cursor={false}
              content={
                <ChartTooltipContent
                  indicator="line"
                  formatter={(value, name) => (
                    <div className="flex min-w-32 items-center justify-between gap-3">
                      <span className="text-muted-foreground">{String(name)}</span>
                      <span className="font-mono font-medium text-foreground">
                        {formatBytes(Number(value))}
                      </span>
                    </div>
                  )}
                />
              }
            />
            <Area
              dataKey="inboundBytes"
              type="natural"
              fill="var(--color-inboundBytes)"
              fillOpacity={0.4}
              stroke="var(--color-inboundBytes)"
            />
            <Area
              dataKey="outboundBytes"
              type="natural"
              fill="var(--color-outboundBytes)"
              fillOpacity={0.25}
              stroke="var(--color-outboundBytes)"
            />
          </AreaChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
