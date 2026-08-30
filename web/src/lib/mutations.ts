import {
  useMutation,
  useQueryClient,
  type MutationScope,
  type QueryKey,
  type UseMutationOptions,
} from "@tanstack/react-query"
import { toast } from "darkraise-ui"
import { ApiError } from "./api"

/**
 * Every mutation reports through the toaster.
 *
 * `Toaster` shipped and went unused: a failed write set a string that rendered
 * in a corner of whichever screen owned it, so an action taken from a drawer
 * could fail somewhere the reader was no longer looking. One path for every
 * mutation means a failure is never silent and never local.
 */
export function useApiMutation<TData, TVars>(opts: {
  mutationFn: (vars: TVars) => Promise<TData>
  /** Shown on success. Omit for an action whose effect is already visible. */
  success?: string | ((data: TData, vars: TVars) => string)
  /** Cache entries the write invalidates. */
  invalidates?: QueryKey[]
  /** Runs writes sharing this id one at a time, in the order they were made.
   *  Without it two writes to the same row are concurrent, and the row keeps
   *  whichever response the server happened to finish last. */
  scope?: MutationScope
  onSuccess?: UseMutationOptions<TData, Error, TVars>["onSuccess"]
}) {
  const queryClient = useQueryClient()
  return useMutation<TData, Error, TVars>({
    mutationFn: opts.mutationFn,
    scope: opts.scope,
    onSuccess: (data, vars, ctx, mutation) => {
      if (opts.success) {
        toast.success(
          typeof opts.success === "function" ? opts.success(data, vars) : opts.success,
        )
      }
      for (const key of opts.invalidates ?? []) {
        void queryClient.invalidateQueries({ queryKey: key })
      }
      opts.onSuccess?.(data, vars, ctx, mutation)
    },
    onError: (err) => {
      // A 401 is handled once, globally, by the unauthorized listener that
      // sends the operator to the login screen. Toasting it too would say
      // "failed" beside a screen that is already explaining itself.
      if (err instanceof ApiError && err.status === 401) return
      toast.error(err.message)
    },
  })
}
