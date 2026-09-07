import { useState, type ChangeEvent } from "react"
import { Badge, Button, Input, Label, Switch } from "darkraise-ui"
import { NumberBox } from "../shell/number-box"
import { SOURCE_LABEL, SOURCE_NOTE, parseBytes, type SettingRow } from "./settings-catalog"

/**
 * One setting, with the editor its kind calls for.
 *
 * Every editor holds and emits the *stored* spelling — "10m0s", "33554432",
 * "true" — because that is what the write path parses back. A field that
 * displayed one spelling and submitted another would round-trip wrong on
 * every save.
 */
export function SettingField({
  row,
  value,
  onChange,
  onReset,
  resetting = false,
  disabled = false,
  error,
}: {
  row: SettingRow
  /** The draft value, in the stored spelling. */
  value: string
  onChange: (next: string) => void
  /** Toggles the row in and out of the save's reset list. Null when the key is
   *  on its default and there is nothing to reset. */
  onReset: (() => void) | null
  /** This row's stored value is being deleted by the next save. The editor is
   *  gated rather than emptied: a stored `true` shown as an unchecked switch
   *  is what "set this to false" looks like, which is a different write. */
  resetting?: boolean
  /** A save is in flight. A keystroke landing now is wiped by the reseed that
   *  follows it, so the box would swallow an edit the operator watched. */
  disabled?: boolean
  /** The server's complaint about this key from the last refused save. */
  error?: string
}) {
  // An emptied box means the same thing as the Reset button: the store cannot
  // hold "" as a value distinct from absent, so the row has to say so rather
  // than letting an empty field read as a value of nothing.
  // The message gets its own flex line rather than a place in the value
  // column: a cross-key refusal runs to ~180 characters, and a column that
  // cannot shrink grows to the width of the card to hold it, breaking the row
  // onto two lines.
  const errorId = error ? `${row.field}-error` : undefined
  const willReset = resetting || (row.editable && row.source === "database" && value.trim() === "")
  return (
    <div className="flex flex-wrap items-start gap-4 border-t py-3 first:border-t-0 first:pt-0">
      <div className="min-w-0 flex-1">
        {row.editable ? (
          <Label htmlFor={row.field} className="font-medium">
            {row.meta.name}
          </Label>
        ) : (
          <p className="font-medium">{row.meta.name}</p>
        )}
        {row.meta.description && (
          <p className="text-sm text-[hsl(var(--muted-foreground))]">{row.meta.description}</p>
        )}
        <p className="font-mono text-sm text-[hsl(var(--legend))]">{row.field}</p>
      </div>
      <div className="flex shrink-0 flex-col items-end gap-1">
        {row.editable ? (
          <Editor
            row={row}
            value={value}
            onChange={onChange}
            disabled={disabled || resetting}
            describedBy={errorId}
          />
        ) : (
          <span className="font-mono text-base font-medium tabular-nums">{row.display}</span>
        )}
        <span className="flex items-center gap-1">
          <Badge variant="outline" title={SOURCE_NOTE[row.source]}>
            {SOURCE_LABEL[row.source]}
          </Badge>
          {/* An environment value gets neither badge. Nothing captures it at
              construction, but a variable cannot change under a running
              process, so "hot" would promise a live edit that is impossible;
              the env chip already says the whole story. */}
          {row.source === "env" ? (
            <Badge variant="secondary" title="Read from the environment at startup">
              env {row.env}
            </Badge>
          ) : row.hotReloadable ? (
            <Badge variant="green">hot</Badge>
          ) : (
            <Badge variant="secondary">restart</Badge>
          )}
          {willReset && <Badge variant="outline">resets to default on save</Badge>}
          {/* The guarantee that an environment row has no reset lives here
              rather than in a convention every caller has to remember. */}
          {row.editable && onReset && (
            <Button
              variant="ghost"
              size="sm"
              onClick={onReset}
              title={
                resetting
                  ? "Leave the stored row alone after all"
                  : "Delete the stored row and fall back to the built-in default"
              }
            >
              {resetting ? "Keep" : "Reset"}
            </Button>
          )}
        </span>
      </div>
      {error && (
        <p id={errorId} className="basis-full text-sm text-[hsl(var(--destructive))]">
          {error}
        </p>
      )}
    </div>
  )
}

function Editor({
  row,
  value,
  onChange,
  disabled,
  describedBy,
}: {
  row: SettingRow
  value: string
  onChange: (next: string) => void
  disabled: boolean
  describedBy?: string
}) {
  switch (row.kind) {
    case "bool":
      return (
        // Named through aria-label rather than by wrapping. Switch renders a
        // `role="switch"` button, and a button takes its accessible name from
        // aria-label or its own subtree, never from an enclosing label.
        <Switch
          id={row.field}
          aria-label={row.meta.name}
          aria-describedby={describedBy}
          checked={value === "true"}
          disabled={disabled}
          onCheckedChange={(next: boolean) => onChange(String(next))}
        />
      )
    case "int":
      return (
        <NumberBox
          id={row.field}
          value={value}
          onChange={onChange}
          disabled={disabled}
          describedBy={describedBy}
          // A gated row is still a row being read: dimming its value to
          // placeholder grey would hide the number the operator is deciding
          // about. The border dims instead.
          retainValue
          step={1}
          precision={0}
          className="w-40 shrink-0"
        />
      )
    case "bytes":
      // Nobody reads 33554432 as 32 MB, so the box shows the scale and emits
      // the bytes. Unparseable text is emitted raw so the server can refuse it
      // and say why, rather than the screen silently dropping the keystroke.
      return (
        <DraftBox
          row={row}
          value={value}
          onChange={onChange}
          disabled={disabled}
          describedBy={describedBy}
          seed={seedBytes}
          emit={(text) => {
            const bytes = parseBytes(text)
            return bytes === undefined ? text : String(bytes)
          }}
        />
      )
    default:
      return (
        <DraftBox
          row={row}
          value={value}
          onChange={onChange}
          disabled={disabled}
          describedBy={describedBy}
          seed={(v) => v}
          emit={(text) => text}
          placeholder={
            row.kind === "url"
              ? row.field === "server.public_url"
                ? "llm.example.com"
                : "https://example.com/api.json"
              : undefined
          }
        />
      )
  }
}

/**
 * The stored spelling of a size, seeded at the scale a person reads.
 *
 * `formatBytes` rounds to one decimal, so 33554433 shows as "32.0 MB" and
 * parses back one byte short. Seeding from the display unconditionally would
 * therefore rewrite a non-round stored value on a save the operator never
 * made, so the display is used only when its round trip is exact.
 */
function seedBytes(value: string, row: SettingRow): string {
  return parseBytes(row.display) === Number(value) ? row.display : value
}

/**
 * A text box that holds a draft.
 *
 * The box owns what is typed, not the prop: an editor whose text was the prop
 * would reformat a half-written value under the cursor. The prop still wins
 * when it changes under the box — a reset, or a refetch — unless it is what
 * the box itself just emitted.
 */
function DraftBox({
  row,
  value,
  onChange,
  disabled,
  describedBy,
  seed,
  emit,
  placeholder,
}: {
  row: SettingRow
  value: string
  onChange: (next: string) => void
  disabled: boolean
  describedBy?: string
  /** The stored value as the box first shows it. */
  seed: (value: string, row: SettingRow) => string
  /** The typed text as the store spells it. */
  emit: (text: string) => string
  placeholder?: string
}) {
  const [state, setState] = useState({ prop: value, emitted: value, text: seed(value, row) })
  let text = state.text
  if (value !== state.prop) {
    text = value === state.emitted ? state.text : seed(value, row)
    setState({ prop: value, emitted: value, text })
  }

  return (
    <Input
      id={row.field}
      value={text}
      aria-describedby={describedBy}
      disabled={disabled}
      placeholder={placeholder}
      onChange={(e: ChangeEvent<HTMLInputElement>) => {
        const next = e.target.value
        const emitted = emit(next)
        setState({ prop: value, emitted, text: next })
        onChange(emitted)
      }}
      className="w-40 shrink-0 font-mono"
    />
  )
}
