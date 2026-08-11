import { Component, type ErrorInfo, type ReactNode } from 'react'

interface Props {
  /** Changing this remounts the children — a new panel deserves a fresh try. */
  resetKey: string
  children: ReactNode
}

interface State {
  message: string | null
}

/**
 * Keeps a panel's failure to itself. React unmounts the whole tree on an
 * uncaught render error, which turned one bad field in a daemon response into
 * an empty window.
 */
export class PanelBoundary extends Component<Props, State> {
  override state: State = { message: null }

  static getDerivedStateFromError(error: unknown): State {
    return { message: error instanceof Error ? error.message : String(error) }
  }

  override componentDidUpdate(previous: Props): void {
    if (previous.resetKey !== this.props.resetKey && this.state.message) {
      this.setState({ message: null })
    }
  }

  override componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error('panel failed:', error, info.componentStack)
  }

  override render(): ReactNode {
    if (this.state.message === null) return this.props.children
    return (
      <div className="panel-error">
        <p>This panel hit an error.</p>
        <p className="muted">{this.state.message}</p>
        <button className="ghost" onClick={() => this.setState({ message: null })}>
          Try again
        </button>
      </div>
    )
  }
}
