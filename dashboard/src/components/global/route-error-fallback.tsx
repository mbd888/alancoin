import type { ErrorComponentProps } from "@tanstack/react-router";

export function RouteErrorFallback({ error, reset }: ErrorComponentProps) {
  const isChunkError =
    error.message?.includes("Failed to fetch dynamically imported module") ||
    error.message?.includes("Loading chunk") ||
    error.message?.includes("Importing a module script failed");

  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center gap-4 px-4 text-center">
      <h1 className="text-lg font-semibold text-foreground">
        {isChunkError ? "Failed to load page" : "Something went wrong"}
      </h1>
      <p className="text-sm text-muted-foreground">
        {isChunkError
          ? "A network error occurred while loading this page."
          : "An unexpected error occurred."}
      </p>
      <button
        onClick={() => (isChunkError ? window.location.reload() : reset())}
        className="text-sm text-accent-foreground underline underline-offset-4 hover:text-foreground"
      >
        {isChunkError ? "Reload page" : "Try again"}
      </button>
    </div>
  );
}
