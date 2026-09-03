# Specs

Design documents. They lead the code: read them for intent, not as a description of
what is merged. Some are superseded and stay here for the history.

| Spec | Title |
| --- | --- |
| [01-concept.md](01-concept.md) | claude-tmux: Concept & Vision |
| [02-tui-design.md](02-tui-design.md) | TUI Design |
| [03-notifications.md](03-notifications.md) | Notification System |
| [04-architecture.md](04-architecture.md) | Architecture |
| [05-cli-interface.md](05-cli-interface.md) | CLI Interface |
| [06-claude-hooks-reference.md](06-claude-hooks-reference.md) | Claude Code Hooks Reference |
| [07-ui-improvements-roadmap.md](07-ui-improvements-roadmap.md) | UI Improvements Roadmap |
| [08-design-decisions.md](08-design-decisions.md) | Design Decisions |
| [09-prerequisites-and-health-checks.md](09-prerequisites-and-health-checks.md) | Prerequisites & Health Checks |
| [10-tmux-resurrect-integration.md](10-tmux-resurrect-integration.md) | tmux-resurrect & continuum Integration |
| [11-notification-page.md](11-notification-page.md) | Notification Page |
| [12-auto-approve.md](12-auto-approve.md) | Per-Session Auto-Approve |
| [13-notification-channels-and-plugins.md](13-notification-channels-and-plugins.md) | Notification Channels & Plugin System |
| [14-remote-commands.md](14-remote-commands.md) | Remote Commands via Channels |
| [15-daemon-architecture.md](15-daemon-architecture.md) | Daemon Architecture |
| [16-http-api.md](16-http-api.md) | HTTP API Protocol |
| [17-naming.md](17-naming.md) | Naming |
| [18-provider-interface.md](18-provider-interface.md) | AI Provider Interface |
| [19-flow-diagrams.md](19-flow-diagrams.md) | Flow Diagrams |
| [20-remote-access-and-auth.md](20-remote-access-and-auth.md) | Remote Access & Authentication |
| [21-channel-protocol.md](21-channel-protocol.md) | Channel Protocol |
| [22-session-management-and-remote-control.md](22-session-management-and-remote-control.md) | Session Management & Remote Control |
| [22-setup-and-security.md](22-setup-and-security.md) | Setup Flow & Security Architecture |
| [23-rich-approval-hitl.md](23-rich-approval-hitl.md) | Generic Rich HITL (Human-in-the-Loop) |
| [24-session-management-tmux.md](24-session-management-tmux.md) | Session Management with tmux |
| [25-device-generated-keys.md](25-device-generated-keys.md) | Device-Generated Keys & Unified `helios start` TUI |
| [26-session-status-fixes.md](26-session-status-fixes.md) | Session Status Fixes: Missing Hooks, Stale Detection, Compaction, Mobile Polling |
| [27-bearer-auth-remove-cookies.md](27-bearer-auth-remove-cookies.md) | Bearer Token Auth: Remove Cookies, Client-Signed JWTs |
| [28-managed-session-recovery.md](28-managed-session-recovery.md) | Managed Session Recovery |
| [29-terminal-host-replacing-tmux.md](29-terminal-host-replacing-tmux.md) | Terminal Host: Replacing tmux |
| [30-tailscale-transport.md](30-tailscale-transport.md) | Tailscale as a First-Class, Recommended Tunnel Provider |
| [31-desktop-app.md](31-desktop-app.md) | Desktop App |
| [32-mobile-notification-lifecycle.md](32-mobile-notification-lifecycle.md) | Mobile Notification Lifecycle: Cancel on Resolve, Reconcile on Resume |
| [33-session-error-retry.md](33-session-error-retry.md) | Session Error Retry: Capture the Error, Unblock the Composer, Offer Retry |
| [34-askuserquestion-dual-answer.md](34-askuserquestion-dual-answer.md) | AskUserQuestion: Answerable From the Terminal *and* the Phone |
| [35-git-history-and-worktrees.md](35-git-history-and-worktrees.md) | Git History and Worktree View |
| [36-helios-owned-hitl.md](36-helios-owned-hitl.md) | Helios-Owned HITL |
| [37-prompt-delivery-reliability.md](37-prompt-delivery-reliability.md) | Prompt Delivery Reliability |
| [38-transcript-incremental-serving.md](38-transcript-incremental-serving.md) | Transcript Serving: Parse Once, Keep It in RAM, Append the Tail |
| [39-agent-driven-explain-ui.md](39-agent-driven-explain-ui.md) | Agent-Driven Explain UI: A Helios MCP Server and the Deck It Draws |
| [40-learnings.md](40-learnings.md) | Learnings: A Folder an Agent Writes, a Library Helios Reads |
| [41-helios-mcp-tools.md](41-helios-mcp-tools.md) | Helios MCP Tools: Show the Human, Reach Other Sessions |
| [42-cold-sessions.md](42-cold-sessions.md) | Cold Sessions: Enforce the Memory Budget |
| [43-shared-resource-leases.md](43-shared-resource-leases.md) | Shared Resource Leases: One Docker Stack, Many Agents |
| [44-transcript-path-relocation.md](44-transcript-path-relocation.md) | Transcript Path Relocation: Follow a Session Into Its Worktree |
| [45-one-archival-state.md](45-one-archival-state.md) | One Archival State: Terminated Replaces Archived |
| [46-codex-provider.md](46-codex-provider.md) | Codex Provider |
| [46-group-ordering.md](46-group-ordering.md) | Grouping: A Tree of Manual Groups |
| [47-provider-interface.md](47-provider-interface.md) | The Provider Interface |
| [48-mobile-provider-support.md](48-mobile-provider-support.md) | Mobile: Support a Second Provider |
| [48-react-query-data-layer.md](48-react-query-data-layer.md) | React Query as the Desktop Data Layer |
| [49-mobile-query-data-layer.md](49-mobile-query-data-layer.md) | A Query Data Layer for the Mobile App |
| [50-desktop-split-layout.md](50-desktop-split-layout.md) | Editor Groups for the Desktop: Two Panels Side by Side, Remembered Per Session |
| [51-file-previews.md](51-file-previews.md) | File Previews: Show the Image, Render the Page |
| [52-transcript-freshness.md](52-transcript-freshness.md) | Transcript Freshness: Heal a Missed Event Without a Spinner |
| [53-mermaid-diagrams.md](53-mermaid-diagrams.md) | Mermaid Diagrams: Draw the Fence Instead of Printing It |
| [54-file-change-events.md](54-file-change-events.md) | File Change Events: Watch What Someone Is Looking At |
| [55-answering-a-question-from-the-terminal.md](55-answering-a-question-from-the-terminal.md) | Answering a Question From the Terminal |
| [56-one-version-number.md](56-one-version-number.md) | One Version Number, and Builds That Do Not Lie About It |
| [57-plan-approval.md](57-plan-approval.md) | Approving a Plan From the Terminal |
| [58-files-without-a-root.md](58-files-without-a-root.md) | Files Without a Root: Folders, an Index, and a Path You Can Type |
| [ai-narration.md](ai-narration.md) | AI Narration for Voice Mode |
| [desktop-notification-handoff.md](desktop-notification-handoff.md) | Hand desktop notifications to the desktop app |
| [desktop-notification-service.md](desktop-notification-service.md) | Desktop Notification Service |
| [multi-host-spec.md](multi-host-spec.md) | Multi-Host Support — Technical Specification |
| [notification-alert-settings.md](notification-alert-settings.md) | Notification Alert Settings |
| [reporter.md](reporter.md) | Reporter: Push-Based AI Narration via SSE |
| [session-search-and-group-by-directory.md](session-search-and-group-by-directory.md) | Session Search, Directory Filter & Session Title |
| [spec-localhostrun-provider.md](spec-localhostrun-provider.md) | localhost.run Tunnel Provider |
| [spec-localtunnel-provider.md](spec-localtunnel-provider.md) | localtunnel Tunnel Provider |
| [spec-localxpose-provider.md](spec-localxpose-provider.md) | localxpose Tunnel Provider |
| [spec-tunnel-decoupling.md](spec-tunnel-decoupling.md) | Tunnel Decoupling & Daemon Crash Recovery |
| [spec-zrok-tunnel-provider.md](spec-zrok-tunnel-provider.md) | zrok Tunnel Provider |
| [voice-mode.md](voice-mode.md) | Voice Mode for Helios Mobile |
