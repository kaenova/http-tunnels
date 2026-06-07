import { useState } from "react"
import { format, subDays } from "date-fns"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type { ChartRange } from "@/lib/api"

interface ChartRangePickerProps {
  value: ChartRange
  onChange: (range: ChartRange) => void
}

export function ChartRangePicker({ value, onChange }: ChartRangePickerProps) {
  const [startDate, setStartDate] = useState(value.startDate)
  const [endDate, setEndDate] = useState(value.endDate)
  const [granularity, setGranularity] = useState(value.granularity)

  const apply = () => {
    onChange({
      granularity,
      startDate,
      endDate,
    })
  }

  const presetLast7Days = () => {
    const end = format(new Date(), "yyyy-MM-dd")
    const start = format(subDays(new Date(), 6), "yyyy-MM-dd")
    setStartDate(start)
    setEndDate(end)
    onChange({ granularity: "day", startDate: start, endDate: end })
  }

  const presetLast30Days = () => {
    const end = format(new Date(), "yyyy-MM-dd")
    const start = format(subDays(new Date(), 29), "yyyy-MM-dd")
    setStartDate(start)
    setEndDate(end)
    onChange({ granularity: "day", startDate: start, endDate: end })
  }

  return (
    <div className="flex flex-wrap items-center gap-3">
      <Select value={granularity} onValueChange={setGranularity}>
        <SelectTrigger className="w-[120px]">
          <SelectValue placeholder="Granularity" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="hour">Hour</SelectItem>
          <SelectItem value="day">Day</SelectItem>
          <SelectItem value="week">Week</SelectItem>
        </SelectContent>
      </Select>

      <div className="flex items-center gap-2">
        <input
          type="date"
          value={startDate}
          onChange={(e) => setStartDate(e.target.value)}
          className="h-9 rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors"
        />
        <span className="text-muted-foreground">to</span>
        <input
          type="date"
          value={endDate}
          onChange={(e) => setEndDate(e.target.value)}
          className="h-9 rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors"
        />
      </div>

      <Button variant="outline" size="sm" onClick={apply}>
        Apply
      </Button>
      <Button variant="ghost" size="sm" onClick={presetLast7Days}>
        Last 7 days
      </Button>
      <Button variant="ghost" size="sm" onClick={presetLast30Days}>
        Last 30 days
      </Button>
    </div>
  )
}
