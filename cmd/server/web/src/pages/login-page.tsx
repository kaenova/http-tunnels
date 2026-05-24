import { useEffect, useMemo, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Navigate, useLocation, useNavigate } from "react-router-dom"
import { LockIcon } from "lucide-react"
import { toast } from "sonner"

import { api, ApiError } from "@/lib/api"
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
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"

export function LoginPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const [password, setPassword] = useState("")
  const [errorMessage, setErrorMessage] = useState("")

  const redirectTo = useMemo(() => {
    const state = location.state as { redirectTo?: string } | null
    return state?.redirectTo || "/admin"
  }, [location.state])

  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: api.session,
    retry: false,
    refetchOnWindowFocus: false,
  })

  const loginMutation = useMutation({
    mutationFn: (value: string) => api.login(value),
    onSuccess: async () => {
      await queryClient.fetchQuery({
        queryKey: ["admin-session"],
        queryFn: api.session,
      })
      toast.success("Authenticated. Redirecting to the admin dashboard.")
      navigate(redirectTo, { replace: true })
    },
    onError: (error: ApiError) => {
      setErrorMessage(error.message)
    },
  })

  useEffect(() => {
    if (loginMutation.isPending) {
      setErrorMessage("")
    }
  }, [loginMutation.isPending])

  if (sessionQuery.isLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center px-6">
        <Card className="w-full max-w-md">
          <CardHeader>
            <Skeleton className="h-6 w-40" />
            <Skeleton className="h-4 w-64" />
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </CardContent>
        </Card>
      </div>
    )
  }

  if (sessionQuery.data?.authenticated) {
    return <Navigate to="/admin" replace />
  }

  const configured = sessionQuery.data?.configured ?? false

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/30 px-6 py-12">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-xl">
            <LockIcon />
            Admin login
          </CardTitle>
          <CardDescription>
            Authenticate with the server-side <code>WEB_PASSWORD</code> value to access
            the HTTP Tunnels admin dashboard.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-col gap-5"
            onSubmit={(event) => {
              event.preventDefault()
              setErrorMessage("")
              loginMutation.mutate(password)
            }}
          >
            <FieldGroup>
              <Field data-invalid={!!errorMessage || !configured || undefined}>
                <FieldLabel htmlFor="password">Password</FieldLabel>
                <FieldContent>
                  <Input
                    id="password"
                    type="password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    placeholder="Enter WEB_PASSWORD"
                    aria-invalid={!!errorMessage || !configured}
                    autoComplete="current-password"
                  />
                  {configured ? (
                    <FieldDescription>
                      This value is validated entirely on the Go server and stored in an
                      authenticated cookie session.
                    </FieldDescription>
                  ) : (
                    <FieldDescription>
                      {sessionQuery.data?.message ||
                        "WEB_PASSWORD is missing on the server. Set it before using the admin dashboard."}
                    </FieldDescription>
                  )}
                  <FieldError>{errorMessage}</FieldError>
                </FieldContent>
              </Field>
            </FieldGroup>
            <Button type="submit" disabled={!configured || loginMutation.isPending}>
              {loginMutation.isPending ? "Authenticating..." : "Login"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
