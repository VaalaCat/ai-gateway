import { cn } from "@/lib/utils";

export interface ResponsiveChartFrameProps {
  children: React.ReactNode;
  legend?: React.ReactNode;
  minHeight?: number;
  aspect?: `${number}/${number}`;
  className?: string;
  legendPlacement?: "bottom" | "responsive-side";
}

const DEFAULT_MIN_HEIGHT = 224;
const DEFAULT_ASPECT = "16/9";

export function ResponsiveChartFrame({
  children,
  legend,
  minHeight = DEFAULT_MIN_HEIGHT,
  aspect = DEFAULT_ASPECT,
  className,
  legendPlacement = "bottom",
}: ResponsiveChartFrameProps) {
  return (
    <div
      data-testid="responsive-chart-frame"
      data-slot="responsive-chart-frame"
      className={cn(
        "flex min-w-0 flex-col gap-3 font-mono tabular-nums",
        legendPlacement === "responsive-side" && "lg:grid lg:grid-cols-[minmax(0,1fr)_minmax(12rem,0.9fr)] lg:items-center",
        className,
      )}
    >
      <div
        data-testid="responsive-chart-plot"
        data-slot="responsive-chart-plot"
        className="relative flex w-full min-w-0 items-center justify-center overflow-hidden max-h-[17.5rem] [&>[data-slot=chart]]:h-full [&>[data-slot=chart]]:w-full"
        style={{
          minHeight: `${minHeight}px`,
          aspectRatio: aspect.replace("/", " / "),
          containerType: "inline-size",
        }}
      >
        {children}
      </div>
      {legend || legendPlacement === "responsive-side" ? (
        <div
          data-testid="responsive-chart-legend"
          data-slot="responsive-chart-legend"
          className={cn("min-w-0", legendPlacement === "responsive-side" && "lg:max-h-[17.5rem] lg:overflow-y-auto")}
        >
          {legend}
        </div>
      ) : null}
    </div>
  );
}
