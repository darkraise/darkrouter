import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "darkraise-ui/components/chart"
import { Area, AreaChart, Line, LineChart, XAxis, YAxis } from "recharts"

type Cell = Record<string, number | string | null>

/** One `--color-<key>` per series, scoped to the container that renders this
 *  config -- see chart-scope.css for why every slot resolves to the same
 *  accent, differentiated by opacity rather than hue. */
function chartConfig(keys: string[]): ChartConfig {
  const config: ChartConfig = {}
  keys.forEach((k, i) => {
    config[k] = { label: k, color: `hsl(var(--chart-${(i % 5) + 1}))` }
  })
  return config
}

export function StackedAreaChart({ data, keys }: { data: Cell[]; keys: string[] }) {
  return (
    <div className="chart-scope h-56">
      <ChartContainer config={chartConfig(keys)} className="h-full w-full">
        <AreaChart data={data}>
          <XAxis dataKey="day" tickLine={false} axisLine={false} fontSize={11} minTickGap={24} />
          <YAxis tickLine={false} axisLine={false} fontSize={11} width={44} />
          <ChartTooltip content={<ChartTooltipContent indicator="dot" />} />
          {keys.map((k, i) => (
            <Area
              key={k}
              dataKey={k}
              type="monotone"
              stackId="usage"
              fill={`var(--color-${k})`}
              stroke={`var(--color-${k})`}
              // Fill, not hue, is what separates the series -- see chart-scope.css.
              fillOpacity={1 - i * 0.15}
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
}: {
  data: Cell[]
  keys: string[]
  /** Kept out of this module: formatCost's null-vs-zero distinction is
   *  usage-screen's business rule, not a charting concern. */
  formatValue: (v: number | null) => string
}) {
  return (
    <div className="chart-scope h-56">
      <ChartContainer config={chartConfig(keys)} className="h-full w-full">
        <LineChart data={data}>
          <XAxis dataKey="day" tickLine={false} axisLine={false} fontSize={11} minTickGap={24} />
          <YAxis
            tickLine={false}
            axisLine={false}
            fontSize={11}
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
          {keys.map((k, i) => (
            <Line
              key={k}
              dataKey={k}
              type="monotone"
              stroke={`var(--color-${k})`}
              strokeWidth={2}
              strokeOpacity={1 - i * 0.15}
              dot={false}
            />
          ))}
        </LineChart>
      </ChartContainer>
    </div>
  )
}
