import type { ReactNode } from "react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
  Button,
} from "darkraise-ui"

type ButtonProps = React.ComponentProps<typeof Button>

/**
 * A button that asks before it acts.
 *
 * Every removal, revocation and switching-off in the console goes through this
 * rather than through a dialog assembled at the call site: nine call sites
 * assembling their own is nine chances for one of them to forget, and the one
 * that forgets is the one that deletes something.
 *
 * `description` says what happens next, not what is about to be clicked. The
 * title already names the thing; the operator is deciding whether they can
 * live with the consequence.
 */
export function ConfirmButton({
  title,
  description,
  confirmLabel,
  onConfirm,
  children,
  destructive,
  disabled,
  ...button
}: {
  title: string
  description: ReactNode
  confirmLabel: string
  onConfirm: () => void
  children: ReactNode
  /** Colours the confirming action as a loss rather than a change. */
  destructive?: boolean
  disabled?: boolean
} & Pick<ButtonProps, "size" | "variant" | "className">) {
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button {...button} disabled={disabled}>
          {children}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={onConfirm}
            className={
              destructive
                ? "bg-[hsl(var(--destructive))] text-[hsl(var(--destructive-foreground))] hover:bg-[hsl(var(--destructive))]/90"
                : undefined
            }
          >
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
