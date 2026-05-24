import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import type { TunnelRecord } from "@/lib/api"

export function DeleteTunnelDialog({
  tunnel,
  open,
  onOpenChange,
  onConfirm,
  isDeleting,
}: {
  tunnel?: TunnelRecord | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
  isDeleting: boolean
}) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete active tunnel</AlertDialogTitle>
          <AlertDialogDescription>
            {tunnel
              ? `This will remove ${tunnel.domain} from the admin side, close the active connection, and keep the recorded analytics history in the database.`
              : "This will remove the selected active tunnel from the admin side."}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={isDeleting}>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm} disabled={isDeleting}>
            {isDeleting ? "Deleting..." : "Delete"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
