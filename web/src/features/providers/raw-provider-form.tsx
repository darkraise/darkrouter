import { useState } from "react"
import { Button, Input, Label, Switch } from "darkraise-ui"

/**
 * Every field the create endpoint accepts, spelled out.
 *
 * This exists for the provider a preset does not cover: nothing here is
 * inferred from a preset, so nothing here should be pre-filled from one —
 * that is what the Browse tab is for.
 */
export function RawProviderForm({ onSubmit }: { onSubmit: (body: Record<string, unknown>) => void }) {
  const [id, setId] = useState("")
  const [kind, setKind] = useState("")
  const [baseUrl, setBaseUrl] = useState("")
  const [authStyle, setAuthStyle] = useState("")
  const [priority, setPriority] = useState("0")
  const [enabled, setEnabled] = useState(true)
  const [region, setRegion] = useState("")
  const [project, setProject] = useState("")
  const [location, setLocation] = useState("")

  function submit() {
    onSubmit({
      id,
      kind,
      base_url: baseUrl,
      auth_style: authStyle,
      priority: Number(priority) || 0,
      enabled,
      region,
      project,
      location,
    })
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="raw-provider-id">Provider ID</Label>
          <Input id="raw-provider-id" value={id} onChange={(e) => setId(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="raw-provider-kind">Kind</Label>
          <Input id="raw-provider-kind" value={kind} onChange={(e) => setKind(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="raw-provider-base-url">Base URL</Label>
          <Input id="raw-provider-base-url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="raw-provider-auth-style">Auth style</Label>
          <Input id="raw-provider-auth-style" value={authStyle} onChange={(e) => setAuthStyle(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="raw-provider-priority">Priority</Label>
          <Input
            id="raw-provider-priority"
            inputMode="numeric"
            value={priority}
            onChange={(e) => setPriority(e.target.value)}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="raw-provider-region">Region</Label>
          <Input id="raw-provider-region" value={region} onChange={(e) => setRegion(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="raw-provider-project">Project</Label>
          <Input id="raw-provider-project" value={project} onChange={(e) => setProject(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="raw-provider-location">Location</Label>
          <Input id="raw-provider-location" value={location} onChange={(e) => setLocation(e.target.value)} />
        </div>
      </div>
      <div className="flex items-center gap-2">
        <Switch id="raw-provider-enabled" checked={enabled} onCheckedChange={setEnabled} />
        <Label htmlFor="raw-provider-enabled">Enabled</Label>
      </div>
      <div>
        <Button onClick={submit} disabled={id === "" || kind === "" || baseUrl === ""}>
          Create
        </Button>
      </div>
    </div>
  )
}
