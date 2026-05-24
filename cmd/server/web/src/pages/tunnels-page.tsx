import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import type { TunnelRecord } from "@/lib/api"
import { api, ApiError } from "@/lib/api"
import { DeleteTunnelDialog } from "@/components/delete-tunnel-dialog"
import { PageHeader } from "@/components/page-header"
import { PageLoading } from "@/components/page-loading"
import { PaginationControls } from "@/components/pagination-controls"
import { TunnelTable } from "@/components/tunnel-table"

const pageSize = 10

export function TunnelsPage() {
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [selectedTunnel, setSelectedTunnel] = useState<TunnelRecord | null>(null)

  const tunnelsQuery = useQuery({
    queryKey: ["tunnels", page, pageSize],
    queryFn: () => api.listTunnels(page, pageSize),
    refetchInterval: 5000,
  })

  const deleteMutation = useMutation({
    mutationFn: (tunnelId: string) => api.deleteTunnel(tunnelId),
    onSuccess: async () => {
      toast.success("Tunnel deleted.")
      setSelectedTunnel(null)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
        queryClient.invalidateQueries({ queryKey: ["tunnels"] }),
      ])
    },
    onError: (error: ApiError) => {
      toast.error(error.message)
    },
  })

  if (tunnelsQuery.isLoading) {
    return <PageLoading />
  }

  if (tunnelsQuery.isError || !tunnelsQuery.data) {
    return (
      <div className="p-6">
        <p className="text-sm text-destructive">
          {(tunnelsQuery.error as Error)?.message ||
            "The tunnel list could not be loaded."}
        </p>
      </div>
    )
  }

  return (
    <div className="flex flex-col">
      <PageHeader
        title="Active Subdomain"
        description="Paginated tunnel registrations with data transfer metrics, request counts, and admin actions."
        breadcrumbs={[
          { label: "Admin", href: "/admin" },
          { label: "Active Subdomain" },
        ]}
      />
      <div className="flex flex-col gap-4 p-6">
        <TunnelTable
          title="Registered and active tunnels"
          description="Each row shows the tunnel subdomain, request-response count, transferred bytes, and lifetime."
          tunnels={tunnelsQuery.data.items}
          onDelete={(tunnel) => setSelectedTunnel(tunnel)}
          deletingId={deleteMutation.isPending ? selectedTunnel?.id : undefined}
        />
        <PaginationControls
          page={tunnelsQuery.data.page}
          totalPages={tunnelsQuery.data.totalPages}
          onPageChange={setPage}
        />
      </div>

      <DeleteTunnelDialog
        tunnel={selectedTunnel}
        open={!!selectedTunnel}
        onOpenChange={(open) => {
          if (!open) {
            setSelectedTunnel(null)
          }
        }}
        onConfirm={() => {
          if (selectedTunnel) {
            deleteMutation.mutate(selectedTunnel.id)
          }
        }}
        isDeleting={deleteMutation.isPending}
      />
    </div>
  )
}
