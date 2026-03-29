import { Component, type ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "./button";

interface Props {
  children: ReactNode;
  fallback?: ReactNode;
}

interface State {
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  render() {
    if (this.state.error) {
      if (this.props.fallback) return this.props.fallback;
      return (
        <div className="flex min-h-[400px] flex-col items-center justify-center gap-4 text-center">
          <div className="flex size-12 items-center justify-center rounded-lg bg-destructive/10">
            <AlertTriangle size={22} strokeWidth={1.5} className="text-destructive" />
          </div>
          <div>
            <h3 className="text-sm font-medium text-foreground">
              Something went wrong
            </h3>
            <p className="mt-1 max-w-sm text-sm text-muted-foreground">
              {this.state.error.message}
            </p>
          </div>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => this.setState({ error: null })}
          >
            Try again
          </Button>
        </div>
      );
    }
    return this.props.children;
  }
}
