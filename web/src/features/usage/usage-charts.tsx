import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "darkraise-ui/components/chart"
import { Area, AreaChart, Line, LineChart, XAxis, YAxis } from "recharts"

type Cell = Record<string, number | string | null>

/** One `--color-<key>` per series, from the ramp chart-scope.css sets: five
 *  hues kept clear of the red, amber and green that mean a state here. */
function chartConfig(keys: string[]): ChartConfig {
  const config: ChartConfig = {}
  keys.forEach((k, i) => {
    config[k] = { label: k, color: `hsl(var(--chart-${(i % 5) + 1}))` }
  })
  return config
}

// Axis text takes its size from chart-scope.css (`--text-sm`), never from a
// number here: a numeric fontSize opts the ticks out of the font-size axis.

export function StackedAreaChart({
  data,
  keys,
  legend = false,
}: {
  data: Cell[]
  keys: string[]
  /** Named series get a legend; a single total does not need one. */
  legend?: boolean
}) {
  return (
    <div className="chart-scope h-56">
      <ChartContainer config={chartConfig(keys)} className="h-full w-full">
        <AreaChart data={data}>
          <XAxis dataKey="day" tickLine={false} axisLine={false} minTickGap={24} />
          <YAxis tickLine={false} axisLine={false} width={44} />
          <ChartTooltip content={<ChartTooltipContent indicator="dot" />} />
          {legend && <ChartLegend content={<ChartLegendContent />} />}
          {keys.map((k) => (
            <Area
              key={k}
              dataKey={k}
              type="monotone"
              stackId="usage"
              fill={`var(--color-${k})`}
              stroke={`var(--color-${k})`}
              fillOpacity={0.35}
            />
          ))}
        </AreaChart>
      </ChartContainer>
    </div>
  )
}

export function CostLineChart({
  data,
  keys,
  formatValue,
  legend = false,
}: {
  data: Cell[]
  keys: string[]
  /** Kept out of this module: the null-vs-zero distinction is the usage
   *  screen's business rule, not a charting concern. */
  formatValue: (v: number | null) => string
  legend?: boolean
}) {
  return (
    <div className="chart-scope h-56">
      <ChartContainer config={chartConfig(keys)} className="h-full w-full">
        <LineChart data={data}>
          <XAxis dataKey="day" tickLine={false} axisLine={false} minTickGap={24} />
          <YAxis
            tickLine={false}
            axisLine={false}
            width={64}
            tickFormatter={(v: number) => formatValue(v)}
          />
          <ChartTooltip
            content={
              <ChartTooltipContent
                indicator="line"
                // A gap point's value arrives as null: formatValue already
                // renders that as unknown rather than as a false $0.00.
                formatter={(v) => formatValue(typeof v === "number" ? v : null)}
              />
            }
          />
          {legend && <ChartLegend content={<ChartLegendContent />} />}
          {keys.map((k) => (
            <Line
              key={k}
              dataKey={k}
              type="monotone"
              stroke={`var(--color-${k})`}
              strokeWidth={2}
              dot={false}
            />
          ))}
        </LineChart>
      </ChartContainer>
    </div>
  )
}
