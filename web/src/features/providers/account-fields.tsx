import { Checkbox, Input, Label, Textarea, ToggleGroup, ToggleGroupItem } from "darkraise-ui"

export type AccountDraft = {
  mode: "single" | "bulk"
  label: string
  secret: string
  bulk: string
  /** Narrows what the next discovery sweep imports for this provider. A
   *  provider setting rather than a property of the keys being added: against
   *  a provider that already exists the dialog seeds it from what that
   *  provider holds and writes it back when it changes. */
  freeModelsOnly: boolean
  /** Probe each key as it is added and keep only the ones that answer. */
  verifyKeys: boolean
}

export const emptyAccounts: AccountDraft = {
  mode: "single",
  label: "",
  secret: "",
  bulk: "",
  freeModelsOnly: false,
  verifyKeys: true,
}

export type ParsedAccount = { label: string; secret: string }

/** What the secret field asks for. Most providers hand out a key; a local CLI
 *  hands out a session document, and asking for an "API key" would send the
 *  operator looking for something their vendor does not issue. */
export type SecretField = {
  label: string
  placeholder: string
  /** A session document is JSON several lines long; a key is one short line. */
  multiline: boolean
  help?: string
}

const API_KEY_FIELD: SecretField = {
  label: "API key",
  placeholder: "sk-…",
  multiline: false,
}

/**
 * The secret field for one preset.
 *
 * Keyed on the preset rather than on the auth style, because the style says
 * how the credential is transmitted and this says what the operator has to go
 * and find — two different questions that happen to coincide for most of the
 * catalogue.
 */
export function secretFieldFor(presetID?: string): SecretField {
  if (presetID === "auggie") {
    return {
      label: "Augment session",
      placeholder: '{"accessToken":"…","tenantURL":"…"}',
      multiline: true,
      help:
        "Run `auggie login` on a machine with a browser, then paste the contents of " +
        "~/.augment/session.json. Leave this empty if you ran `auggie login` inside " +
        "the container instead — the CLI keeps its own session and needs nothing here.",
    }
  }
  return API_KEY_FIELD
}

/**
 * One account per non-empty line, in `name|key` form.
 *
 * The name is optional: a line with no pipe is all key, which is what a
 * column pasted out of a password manager looks like. A key containing a pipe
 * survives, because only the first one splits.
 *
 * Duplicates are dropped by secret rather than by name: pasting a column
 * twice is a slip, and two credentials holding one key cool and fail as one
 * while presenting as two working accounts. Surrounding quotes and commas
 * come off because a paste out of a CSV or a JSON array brings them along.
 */
export function parseBulkAccounts(text: string, prefix = "key"): ParsedAccount[] {
  const seen = new Set<string>()
  const out: ParsedAccount[] = []
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim()
    if (line === "") continue
    const pipe = line.indexOf("|")
    const clean = (v: string) =>
      v.trim().replace(/,$/, "").replace(/^["']|["']$/g, "").trim()
    const name = pipe === -1 ? "" : clean(line.slice(0, pipe))
    const secret = clean(pipe === -1 ? line : line.slice(pipe + 1))
    if (secret === "" || seen.has(secret)) continue
    seen.add(secret)
    out.push({ label: name || `${prefix}-${out.length + 1}`, secret })
  }
  return out
}

/** Enough to recognise a key in the preview, never enough to use one. */
export function maskSecret(secret: string): string {
  if (secret.length <= 8) return "•".repeat(secret.length)
  return `${secret.slice(0, 4)}${"•".repeat(6)}${secret.slice(-4)}`
}

/** What the draft would create, in the order it will be written. */
export function draftAccounts(draft: AccountDraft): ParsedAccount[] {
  if (draft.mode === "single") {
    const secret = draft.secret.trim()
    return secret === "" ? [] : [{ label: draft.label.trim() || "default", secret }]
  }
  return parseBulkAccounts(draft.bulk, draft.label.trim() || "key")
}

/**
 * The account step, shared by the wizard and the provider detail page.
 *
 * Single and bulk are one segmented control rather than two forms: they
 * produce the same thing, and an operator with three keys should not have to
 * find a different screen from the one with one key.
 */
export function AccountFields({
  value,
  onChange,
  autoFocus,
  field = API_KEY_FIELD,
}: {
  value: AccountDraft
  onChange: (next: AccountDraft) => void
  autoFocus?: boolean
  /** What this provider's secret is called and looks like. */
  field?: SecretField
}) {
  const parsed = parseBulkAccounts(value.bulk, value.label.trim() || "key")
  return (
    <div className="flex flex-col gap-4">
      <ToggleGroup
        type="single"
        value={value.mode}
        onValueChange={(mode) => {
          // An empty value comes back when the pressed item is pressed again;
          // a mode has to be one thing or the other, so that is ignored.
          if (mode === "single" || mode === "bulk") onChange({ ...value, mode })
        }}
        aria-label="How many accounts to add"
        className="w-fit rounded-[var(--radius)] border bg-[hsl(var(--muted))] p-0.5"
      >
        <ToggleGroupItem value="single">Single account</ToggleGroupItem>
        <ToggleGroupItem value="bulk">Bulk import</ToggleGroupItem>
      </ToggleGroup>

      {value.mode === "single" ? (
        <div className="flex flex-wrap items-end gap-2">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-label">Label</Label>
            <Input
              id="account-label"
              value={value.label}
              onChange={(e) => onChange({ ...value, label: e.target.value })}
              placeholder="default"
              className="w-40"
            />
          </div>
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <Label htmlFor="account-secret">{field.label}</Label>
            {field.multiline ? (
              // A session document does not fit a one-line password box, and
              // pasting one into an input that hides what arrived is how a
              // truncated paste goes unnoticed until the first request fails.
              <Textarea
                id="account-secret"
                rows={4}
                autoFocus={autoFocus}
                value={value.secret}
                onChange={(e) => onChange({ ...value, secret: e.target.value })}
                placeholder={field.placeholder}
                className="font-mono text-sm"
              />
            ) : (
              <Input
                id="account-secret"
                type="password"
                autoFocus={autoFocus}
                value={value.secret}
                onChange={(e) => onChange({ ...value, secret: e.target.value })}
                placeholder={field.placeholder}
                className="w-72"
              />
            )}
            {field.help && (
              <p className="max-w-prose text-sm text-[hsl(var(--legend))]">{field.help}</p>
            )}
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-bulk">Accounts, one per line</Label>
            <Textarea
              id="account-bulk"
              rows={12}
              value={value.bulk}
              onChange={(e) => onChange({ ...value, bulk: e.target.value })}
              placeholder={"work|sk-aaa…\nspare|sk-bbb…\nsk-ccc…"}
              className="font-mono text-sm"
            />
            <p className="text-sm text-[hsl(var(--legend))]">
              <span className="font-mono">name|key</span>, or just the key on its own.
            </p>
          </div>
          <div className="flex flex-wrap items-end gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="account-bulk-label">Label prefix</Label>
              <Input
                id="account-bulk-label"
                value={value.label}
                onChange={(e) => onChange({ ...value, label: e.target.value })}
                placeholder="key"
                className="w-40"
              />
              <span className="text-sm text-[hsl(var(--legend))]">
                For the lines that name no account.
              </span>
            </div>
            {/* The count and the masked heads confirm the paste landed the way
                it looked: a stray wrapped line shows up here as one account too
                many, before anything is sent. */}
            <p className="text-sm text-[hsl(var(--legend))]">
              {parsed.length === 0
                ? "Nothing to import yet"
                : `${parsed.length} ${parsed.length === 1 ? "account" : "accounts"} · ${parsed
                    .slice(0, 3)
                    .map((a) => `${a.label} ${maskSecret(a.secret)}`)
                    .join(", ")}${parsed.length > 3 ? " …" : ""}`}
            </p>
          </div>
        </div>
      )}

      <div className="flex flex-col gap-3">
        <div className="flex items-start gap-2">
          <Checkbox
            id="account-free-only"
            checked={value.freeModelsOnly}
            onCheckedChange={(next) =>
              onChange({ ...value, freeModelsOnly: next === true })
            }
          />
          <div className="flex flex-col">
            <Label htmlFor="account-free-only">Import free models only</Label>
            <span className="text-sm text-[hsl(var(--legend))]">
              A discovery sweep keeps a model the provider's own free tier documents,
              one priced at zero, or one tagged <span className="font-mono">:free</span>.
              A model nobody has priced and no free tier covers is not imported —
              unpriced is not free.
            </span>
          </div>
        </div>

        <div className="flex items-start gap-2">
          <Checkbox
            id="account-verify"
            checked={value.verifyKeys}
            onCheckedChange={(next) => onChange({ ...value, verifyKeys: next === true })}
          />
          <div className="flex flex-col">
            <Label htmlFor="account-verify">Check every key before keeping it</Label>
            <span className="text-sm text-[hsl(var(--legend))]">
              Each key is probed as it is added, and any the provider refuses is
              removed again. Slower, and the only way a bad key is caught here rather
              than by the first request that needed it.
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}
