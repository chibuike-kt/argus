import { Panel } from "@/components/ui/panel";
import { ChartToolbar } from "@/components/workspace/chart-toolbar";
import { ToolRail } from "@/components/workspace/tool-rail";
import { RightPanel } from "@/components/workspace/right-panel";
import { ChartSurface } from "@/components/chart/chart-surface";
import { DrawingProvider } from "@/components/chart/drawing-context";

// The analysis workspace: a single chart toolbar on top, the drawing-tool rail
// running full height on the left, the chart filling the center (its own legend,
// axes and bottom bar), and the markets / symbol panel on the right. The
// DrawingProvider shares the active tool and drawn shapes between the rail and
// the chart.
export function AnalysisPreview() {
  return (
    <DrawingProvider>
      <div className="flex h-full flex-col gap-1.5 p-1.5">
        <ChartToolbar />

        <div className="flex min-h-0 flex-1 gap-1.5">
          <ToolRail />

          <Panel
            variant="strong"
            pad="none"
            className="argus-in flex min-w-0 flex-1 flex-col rounded-2xl"
            style={{ "--argus-in-delay": "80ms" } as React.CSSProperties}
          >
            <ChartSurface />
          </Panel>

          <RightPanel />
        </div>
      </div>
    </DrawingProvider>
  );
}
