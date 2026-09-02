import { Link } from "@tanstack/react-router"
import { Button } from "darkraise-ui"
import { EmptyState } from "./empty-state"

/** A path the router does not know. The rail is still up, so this only has
 *  to say so and offer the one place every path starts from. */
export function NotFoundScreen() {
  return (
    <EmptyState
      title="There is no page at this address"
      hint="The link may be out of date, or the address mistyped. Everything the console shows is reachable from the rail."
      action={
        <Button asChild size="sm">
          <Link to="/">Go to the overview</Link>
        </Button>
      }
    />
  )
}
