import {
  Button, Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Textarea,
} from "darkraise-ui"
import {
  FileUpload,
  FileUploadDropzone,
  FileUploadHiddenInput,
  FileUploadTrigger,
} from "darkraise-ui/components/file-upload"
import { Play, Square } from "lucide-react"
import { NumberBox } from "../../shell/number-box"
import { auxBlocker, SURFACE_FIELDS, type FieldSpec, type FormSurface } from "./surfaces"
import type { AuxSurface } from "../../../lib/api-types"

/**
 * What a tool is being asked, docked at the foot of its island.
 *
 * The same place Chat's composer sits, for the same reason: this is where the
 * operator is typing, and a form floating above an empty panel made the run
 * the top of the screen rather than the bottom of it.
 *
 * Every field is labelled. They were placeholders, which vanish at the first
 * keystroke — so a filled form was a column of values with nothing saying
 * which was the count and which was the size.
 */
export function ToolInputs({
  surface,
  needsFile,
  form,
  busy,
  onField,
  onFile,
  onRun,
}: {
  surface: AuxSurface
  needsFile: boolean
  form: Record<string, string>
  busy: boolean
  onField: (key: string, value: string) => void
  onFile: (file: File) => void
  onRun: () => void
}) {
  const fields: FieldSpec[] = needsFile ? [] : SURFACE_FIELDS[surface as FormSurface]
  const primaryKey = fields.find((f) => f.primary)?.key
  const blocker = auxBlocker(surface, form)
  const ready = blocker === null

  // Drawn in the order each tool declares, not with the primary hoisted to
  // the top. Rerank asks for a query and then the documents it ranks; a rule
  // that put the big box first inverted the sentence the form is making.
  function field(f: FieldSpec) {
    const id = `aux-${surface}-${f.key}`
    return (
      <div
        key={f.key}
        className={f.multiline ? "flex flex-col gap-1.5" : "flex min-w-[10rem] flex-col gap-1.5"}
      >
        <Label htmlFor={id}>{f.label}</Label>
        {f.options ? (
          <Select value={form[f.key] ?? ""} onValueChange={(value) => onField(f.key, value)}>
            <SelectTrigger id={id}>
              <SelectValue placeholder={`Choose ${f.label.toLowerCase()}`} />
            </SelectTrigger>
            <SelectContent>
              {f.options.map((option) => (
                <SelectItem key={option} value={option}>{option}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : f.multiline ? (
          <Textarea
            id={id}
            rows={3}
            placeholder={f.placeholder}
            value={form[f.key] ?? ""}
            onChange={(e) => onField(f.key, e.target.value)}
            // Enter runs, Shift+Enter makes a newline -- the binding Chat's
            // composer uses. Not on a field whose own separator is the
            // newline, where Enter would fire the run mid-list.
            onKeyDown={(e) => {
              if (f.key !== primaryKey || f.key === "documents") return
              if (e.key !== "Enter" || e.shiftKey) return
              if (busy || !ready) return
              e.preventDefault()
              onRun()
            }}
            className="resize-none"
          />
        ) : f.type === "number" ? (
          <NumberBox
            id={id}
            precision={0}
            value={form[f.key] ?? ""}
            onChange={(next) => onField(f.key, next)}
          />
        ) : (
          <Input
            id={id}
            placeholder={f.placeholder}
            value={form[f.key] ?? ""}
            onChange={(e) => onField(f.key, e.target.value)}
          />
        )}
        {f.hint ? <p className="text-sm text-[hsl(var(--legend))]">{f.hint}</p> : null}
      </div>
    )
  }

  // The wide fields stack; the narrow settings share a row, because they are
  // almost always left alone and a full-width box each says otherwise.
  const rows: FieldSpec[][] = []
  for (const f of fields) {
    const last = rows[rows.length - 1]
    if (!f.multiline && last && last.every((x) => !x.multiline)) last.push(f)
    else rows.push([f])
  }

  return (
    <div className="flex flex-col gap-3">
      {needsFile ? (
        <FileUpload
          maxFiles={1}
          accept="audio/*"
          onFileAccept={({ files }) => {
            const file = files[0]
            if (file) onFile(file)
          }}
        >
          <FileUploadDropzone className="flex-col gap-2">
            <span className="text-sm text-[hsl(var(--muted-foreground))]">
              Drop an audio file here
            </span>
            <FileUploadTrigger>Choose a file</FileUploadTrigger>
          </FileUploadDropzone>
          {/* The chosen name comes from the parent's own form state rather
              than from the component's file list: the surface keeps one
              record of what will be sent, and two would disagree the moment
              a run cleared it. */}
          {form.filename ? (
            <p className="pt-2 text-sm text-[hsl(var(--muted-foreground))]">{form.filename}</p>
          ) : null}
          <FileUploadHiddenInput />
        </FileUpload>
      ) : null}

      {rows.map((row, i) =>
        row.length === 1 && row[0] !== undefined ? (
          field(row[0])
        ) : (
          <div key={i} className="flex flex-wrap items-start gap-3">
            {row.map(field)}
          </div>
        ),
      )}

      {/* Right-aligned and sized to its own label. A full-width primary bar
          across the panel read as the page's main action when it is one
          tool's. */}
      <div className="flex items-center justify-end gap-3">
        {/* Said beside the button rather than left to be worked out from a
            control that does not answer. */}
        {blocker !== null ? (
          <p className="text-sm text-[hsl(var(--legend))]">{blocker}</p>
        ) : null}
        <Button onClick={onRun} disabled={busy || !ready}>
          {busy ? (
            <Square className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
          ) : (
            <Play className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
          )}
          {busy ? "Running…" : "Run"}
        </Button>
      </div>
    </div>
  )
}
