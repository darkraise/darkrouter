import { PageHeader } from "darkraise-ui/layout"
import { Card } from "darkraise-ui"

/**
 * Scaffolding for a destination whose screen has not been built yet.
 *
 * The rail carries §5's full information architecture from the start, so the
 * shell can be judged as a whole rather than growing an item at a time. A
 * destination that is not ready says so plainly: a screen that renders an
 * empty table instead reads as a broken gateway.
 *
 * Every one of these is removed by the time the console is finished. One
 * surviving into a release is the failure §5 describes — a console with
 * twenty-three sections of which six are stubs.
 */
export function NotBuilt({ title }: { title: string }) {
  return (
    <>
      <PageHeader title={title} />
      <Card className="p-6">
        <p className="text-[hsl(var(--muted-foreground))]">
          This screen is not built yet.
        </p>
      </Card>
    </>
  )
}
