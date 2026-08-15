import { cn } from "@/lib/utils";

function formatTraceContent(content: string): string {
  try {
    return JSON.stringify(JSON.parse(content), null, 2);
  } catch {
    return content;
  }
}

export function TraceContentBlock({
  content,
  className,
}: {
  content: string;
  className?: string;
}) {
  return (
    <pre
      className={cn(
        "max-h-60 w-full min-w-0 max-w-full overflow-auto whitespace-pre font-mono text-xs",
        className,
      )}
    >
      {formatTraceContent(content)}
    </pre>
  );
}
