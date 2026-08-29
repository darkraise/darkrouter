import { Input, Label, Switch, Textarea } from "darkraise-ui"
import { GatedField } from "./gated-field"
import { reasonFor } from "../dialect-support"
import type { PlaygroundConfig } from "../config"

export function Sampling({
  config,
  onChange,
}: {
  config: PlaygroundConfig
  onChange: (next: PlaygroundConfig) => void
}) {
  const set = <K extends keyof PlaygroundConfig>(key: K, value: PlaygroundConfig[K]) =>
    onChange({ ...config, [key]: value })
  const why = (control: Parameters<typeof reasonFor>[1]) => reasonFor(config.dialect, control)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex gap-2">
        <GatedField reason={why("temperature")}>
          <Label htmlFor="pg-temp">Temperature</Label>
          <Input
            id="pg-temp" type="number" placeholder="default"
            disabled={why("temperature") !== null}
            value={config.temperature}
            onChange={(e) => set("temperature", e.target.value)}
          />
        </GatedField>
        <GatedField reason={why("maxTokens")}>
          <Label htmlFor="pg-max">Max tokens</Label>
          <Input
            id="pg-max" type="number" placeholder="default"
            disabled={why("maxTokens") !== null}
            value={config.maxTokens}
            onChange={(e) => set("maxTokens", e.target.value)}
          />
        </GatedField>
      </div>

      <div className="flex gap-2">
        <GatedField reason={why("topP")}>
          <Label htmlFor="pg-topp">Top P</Label>
          <Input
            id="pg-topp" type="number" step="0.01" placeholder="default"
            disabled={why("topP") !== null}
            value={config.topP}
            onChange={(e) => set("topP", e.target.value)}
          />
        </GatedField>
        <GatedField reason={why("topK")}>
          <Label htmlFor="pg-topk">Top K</Label>
          <Input
            id="pg-topk" type="number" placeholder="default"
            disabled={why("topK") !== null}
            value={config.topK}
            onChange={(e) => set("topK", e.target.value)}
          />
        </GatedField>
      </div>

      <GatedField reason={why("stop")}>
        <Label htmlFor="pg-stop">Stop sequences</Label>
        <Textarea
          id="pg-stop" rows={2} placeholder="one per line"
          disabled={why("stop") !== null}
          value={config.stopRaw}
          onChange={(e) => set("stopRaw", e.target.value)}
          className="font-mono text-sm"
        />
      </GatedField>

      <label className="flex items-center gap-2 text-sm">
        <Switch checked={config.stream} onCheckedChange={(next) => set("stream", next)} />
        Stream the reply
      </label>
    </div>
  )
}
