import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { RotateCcwIcon } from "lucide-react"

import type { RequestActivityFilters } from "@/lib/api"
import { api } from "@/lib/api"
import { PageHeader } from "@/components/page-header"
import { PageLoading } from "@/components/page-loading"
import { PaginationControls } from "@/components/pagination-controls"
import { RequestActivityTable } from "@/components/request-activity-table"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Field,
  FieldContent,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const pageSize = 15
const defaultFilters: RequestActivityFilters = {
  search: "",
  method: "",
  statusClass: "",
}

const methods = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"]
const statusClasses = ["2XX", "3XX", "4XX", "5XX"]

export function RequestActivityPage() {
  const [page, setPage] = useState(1)
  const [draftFilters, setDraftFilters] =
    useState<RequestActivityFilters>(defaultFilters)
  const [filters, setFilters] = useState<RequestActivityFilters>(defaultFilters)

  const requestActivityQuery = useQuery({
    queryKey: ["request-activity", page, pageSize, filters],
    queryFn: () => api.listRequestActivity(page, pageSize, filters),
    refetchInterval: 5000,
  })

  if (requestActivityQuery.isLoading) {
    return <PageLoading />
  }

  if (requestActivityQuery.isError || !requestActivityQuery.data) {
    return (
      <div className="p-6">
        <p className="text-sm text-destructive">
          {(requestActivityQuery.error as Error)?.message ||
            "The request activity page could not be loaded."}
        </p>
      </div>
    )
  }

  return (
    <div className="flex flex-col">
      <PageHeader
        title="Request Activity"
        description="Review request-response traffic captured by the tunnel server across every subdomain."
        breadcrumbs={[
          { label: "Admin", href: "/admin" },
          { label: "Request Activity" },
        ]}
      />
      <div className="flex flex-col gap-6 p-6">
        <Card>
          <CardHeader>
            <CardTitle>Filters</CardTitle>
            <CardDescription>
              Filter by request path or domain, method, and status class.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form
              className="flex flex-col gap-4"
              onSubmit={(event) => {
                event.preventDefault()
                setPage(1)
                setFilters(draftFilters)
              }}
            >
              <FieldGroup className="flex flex-col gap-4 xl:flex-row xl:items-end">
                <Field className="flex-1">
                  <FieldLabel htmlFor="request-activity-search">Search</FieldLabel>
                  <FieldContent>
                    <Input
                      id="request-activity-search"
                      value={draftFilters.search}
                      onChange={(event) =>
                        setDraftFilters((current) => ({
                          ...current,
                          search: event.target.value,
                        }))
                      }
                      placeholder="Search by request ID, domain, or path"
                    />
                  </FieldContent>
                </Field>
                <Field>
                  <FieldLabel htmlFor="request-activity-method">Method</FieldLabel>
                  <FieldContent>
                    <Select
                      value={draftFilters.method || "all"}
                      onValueChange={(value) =>
                        setDraftFilters((current) => ({
                          ...current,
                          method: value === "all" ? "" : value,
                        }))
                      }
                    >
                      <SelectTrigger id="request-activity-method" className="w-full min-w-40">
                        <SelectValue placeholder="All methods" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value="all">All methods</SelectItem>
                          {methods.map((method) => (
                            <SelectItem key={method} value={method}>
                              {method}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </FieldContent>
                </Field>
                <Field>
                  <FieldLabel htmlFor="request-activity-status">Status class</FieldLabel>
                  <FieldContent>
                    <Select
                      value={draftFilters.statusClass || "all"}
                      onValueChange={(value) =>
                        setDraftFilters((current) => ({
                          ...current,
                          statusClass: value === "all" ? "" : value,
                        }))
                      }
                    >
                      <SelectTrigger id="request-activity-status" className="w-full min-w-40">
                        <SelectValue placeholder="All status classes" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value="all">All status classes</SelectItem>
                          {statusClasses.map((statusClass) => (
                            <SelectItem key={statusClass} value={statusClass}>
                              {statusClass}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </FieldContent>
                </Field>
                <div className="flex items-center gap-2 xl:pb-px">
                  <Button type="submit">Apply filters</Button>
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => {
                      setDraftFilters(defaultFilters)
                      setFilters(defaultFilters)
                      setPage(1)
                    }}
                  >
                    <RotateCcwIcon data-icon="inline-start" />
                    Reset
                  </Button>
                </div>
              </FieldGroup>
            </form>
          </CardContent>
        </Card>

        <RequestActivityTable logs={requestActivityQuery.data.items} />

        <PaginationControls
          page={requestActivityQuery.data.page}
          totalPages={requestActivityQuery.data.totalPages}
          onPageChange={setPage}
        />
      </div>
    </div>
  )
}
