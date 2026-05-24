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
import type { StatusChartPoint } from "@/lib/api"

const chartConfig = {
  twoXX: {
    label: "2XX",
    color: "var(--chart-1)",
  },
  threeXX: {
    label: "3XX",
    color: "var(--chart-2)",
  },
  fourXX: {
    label: "4XX",
    color: "var(--chart-3)",
  },
  fiveXX: {
    label: "5XX",
    color: "var(--chart-4)",
  },
} satisfies ChartConfig

export function StatusChartCard({ data }: { data: StatusChartPoint[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Status code classes</CardTitle>
        <CardDescription>
          Request volume grouped by 2XX, 3XX, 4XX, and 5XX responses.
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
              content={<ChartTooltipContent indicator="line" />}
            />
            <Area
              dataKey="twoXX"
              type="natural"
              fill="var(--color-twoXX)"
              fillOpacity={0.4}
              stroke="var(--color-twoXX)"
            />
            <Area
              dataKey="threeXX"
              type="natural"
              fill="var(--color-threeXX)"
              fillOpacity={0.35}
              stroke="var(--color-threeXX)"
            />
            <Area
              dataKey="fourXX"
              type="natural"
              fill="var(--color-fourXX)"
              fillOpacity={0.3}
              stroke="var(--color-fourXX)"
            />
            <Area
              dataKey="fiveXX"
              type="natural"
              fill="var(--color-fiveXX)"
              fillOpacity={0.25}
              stroke="var(--color-fiveXX)"
            />
          </AreaChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
