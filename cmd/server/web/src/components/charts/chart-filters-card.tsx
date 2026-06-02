import type { ChartFilters, ChartRangePreset } from "@/lib/api"
import {
  chartGranularityOptions,
  chartRangeOptions,
  defaultCustomChartRange,
  fromDateTimeLocalValue,
  toDateTimeLocalValue,
} from "@/lib/chart-options"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldContent, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export function ChartFiltersCard({
  value,
  onChange,
  onApply,
  onReset,
}: {
  value: ChartFilters
  onChange: (next: ChartFilters) => void
  onApply: () => void
  onReset: () => void
}) {
  const showCustomRange = value.range === "custom"

  return (
    <Card>
      <CardHeader>
        <CardTitle>Chart filters</CardTitle>
      </CardHeader>
      <CardContent>
        <FieldGroup className="flex flex-col gap-4 xl:flex-row xl:flex-wrap xl:items-end">
          <Field className="xl:min-w-48">
            <FieldLabel htmlFor="chart-granularity">Granularity</FieldLabel>
            <FieldContent>
              <Select
                value={value.granularity}
                onValueChange={(next) =>
                  onChange({
                    ...value,
                    granularity: next as ChartFilters["granularity"],
                  })
                }
              >
                <SelectTrigger id="chart-granularity" className="w-full min-w-40">
                  <SelectValue placeholder="Select granularity" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {chartGranularityOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldContent>
          </Field>

          <Field className="xl:min-w-56">
            <FieldLabel htmlFor="chart-range">Time range</FieldLabel>
            <FieldContent>
              <Select
                value={value.range}
                onValueChange={(next) => {
                  const range = next as ChartRangePreset
                  if (range === "custom") {
                    onChange({
                      ...value,
                      range,
                      ...(value.start && value.end ? {} : defaultCustomChartRange()),
                    })
                    return
                  }
                  onChange({
                    granularity: value.granularity,
                    range,
                  })
                }}
              >
                <SelectTrigger id="chart-range" className="w-full min-w-48">
                  <SelectValue placeholder="Select time range" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {chartRangeOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldContent>
          </Field>

          {showCustomRange ? (
            <>
              <Field className="xl:min-w-56">
                <FieldLabel htmlFor="chart-start">Start</FieldLabel>
                <FieldContent>
                  <Input
                    id="chart-start"
                    type="datetime-local"
                    value={toDateTimeLocalValue(value.start)}
                    onChange={(event) =>
                      onChange({
                        ...value,
                        start: fromDateTimeLocalValue(event.target.value),
                      })
                    }
                  />
                </FieldContent>
              </Field>

              <Field className="xl:min-w-56">
                <FieldLabel htmlFor="chart-end">End</FieldLabel>
                <FieldContent>
                  <Input
                    id="chart-end"
                    type="datetime-local"
                    value={toDateTimeLocalValue(value.end)}
                    onChange={(event) =>
                      onChange({
                        ...value,
                        end: fromDateTimeLocalValue(event.target.value),
                      })
                    }
                  />
                </FieldContent>
              </Field>
            </>
          ) : null}

          <div className="flex items-center gap-2 xl:pb-px">
            <Button type="button" onClick={onApply}>
              Apply filters
            </Button>
            <Button type="button" variant="outline" onClick={onReset}>
              Reset
            </Button>
          </div>
        </FieldGroup>
      </CardContent>
    </Card>
  )
}
