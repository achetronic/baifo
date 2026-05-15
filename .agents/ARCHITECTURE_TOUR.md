# Bird's-Eye Architecture Tour of Baifo

Welcome to the internals of **baifo**. This document is a technical map designed to help you quickly understand how information flows through the system: from the moment you press a key in the interface, to an LLM deciding to execute a command on your operating system, and how that response returns perfectly rendered to your screen without losing a single pixel or duplicating a tool call.

Baifo's design orbits around three key concepts: **isolation, extreme concurrency, and intelligent event deduplication**.

---

## 1. The Ecosystem: Actors and Layers

The system is divided into layers with strictly segregated responsibilities:

### A. The Graphical Interface (TUI)
Located in `internal/tui/`, this is the "dumb" layer of the application. Its sole job is to take in-memory data models and paint them on the terminal screen using `bubbletea`. It does not process AI, does not query databases, and does not execute tools. 

### B. The Orchestrator (App & Facade)
Located in `internal/app/`, this is the node's brain. It reads `.yaml` files, boots local databases (SQLite), and connects the wires. Towards the TUI, it exposes a highly restrictive `Facade` (`internal/facade/`) that only permits high-level operations (send text, kill a sub-agent, subscribe to events). This guarantees that if the TUI is replaced by a Web UI tomorrow, the core logic remains completely untouched.

### C. The A2A Engine (Agent-To-Agent)
The true heart of the LLMs. Baifo does not talk directly to OpenAI or Anthropic using raw HTTP requests. It uses the Google ADK framework (`google.golang.org/adk/server/adka2a`), which wraps LLM calls in a robust engine capable of managing tool execution, conversation state, and live streaming.

---

## 2. The Flow of a Message (The Root Agent)

The *Root* is the primary agent, the one you face when you open the application, and it never dies. Furthermore, **the Root is the public face of the server** (it can speak A2A HTTP to the outside world).

1. **Input:** You type "Run an `ls`" in the TUI input box and press Enter.
2. **Dispatch:** The TUI passes it to the Facade (`m.facade.SendMessage(...)`), which injects it directly into the Root's in-memory A2A handler.
3. **Generation:** The A2A engine sends the order to the provider (Gemini, Claude...). The model responds: *"Sure, I'll use the tool."*
4. **Deduplicated Streaming:** The A2A engine features complex "delta parsing" logic. It delivers pure incremental events through a channel, guaranteeing tools and text chunks are emitted cleanly without overlaps.
5. **Live Rendering:** The TUI receives these deltas. It knows exactly which bubble (tracked by `streamingIdx`) needs to receive the text, growing the window progressively and painting the Tool *Cards* inline.

---

## 3. Extreme Multitasking (Workers)

This is where baifo becomes a beast. When you want to do two things at once, you don't ask the Root to wait; you ask it to spawn a **Worker** (a sub-agent). 

Located in `internal/workers/`, Workers are ephemeral, network-blind, and highly optimized.

### How do they work under the hood?
**Every Worker boots its own internal, isolated A2A server in RAM**. 
When you spawn 10 sub-agents, baifo is running 10 A2A engines in independent Go routines. They benefit from the exact same intelligent, deduplicated event stream as the Root agent, ensuring perfectly clean UI rendering.

### The Atomic EventBus
To prevent workers from blocking the main program while they vomit thousands of events (logs, thoughts, tool results), they flush all their output to an `EventBus` (`internal/workers/events.go`).
This bus is completely free of *Mutexes* in its critical path. It uses hardware-level atomic counters (`sync/atomic`), allowing microsecond write speeds regardless of how many agents are actively screaming at the same time.

---

## 4. "Cache" Philosophy: The Secret of TUI Tabs

In baifo, you can switch conversations between the Root and various Workers using commands like `/chat worker_id`.

What happens if the Root is writing a massive poem and you switch tabs to see what Worker 3 is doing? **Nothing is lost. Nothing stops.**

The TUI (`model.go` and `interlocutor.go`) is designed around a model of **Isolated History Caches**:
1. There is a massive in-memory map called `m.chatHistories` that stores the exact visual state of ALL living agents.
2. If an agent sends a chunk of text and you are **not** looking at it, the TUI takes that text, parses it, mathematically concatenates it into that agent's hidden array, and forgets about it. It never attempts to paint it on your current screen.
3. When you decide to return to that agent's tab, the TUI simply swaps the active array pointer. 
4. The result: Instant context switches, zero database queries to redraw history, and absolutely no message bleeding from one chat to another.

---

## 5. Lifecycle and "Zero Polling"

How does the Root (or the TUI) know that a background Worker has finished its task so it can notify you?
Baifo is designed with a strict **Zero Polling** rule. The program never spends CPU cycles asking if a job is done.

1. There is a global `WorkerLifecycleEvent` bus.
2. When a Worker finishes, dies, or crashes, it closes its native Go channel (`w.done`).
3. The garbage collector (`Manager.Collect` in `manager.go`) is sleeping. When the channel closes, it wakes up instantly—having consumed zero clock cycles while waiting—emits the death event to the global bus, and goes back to sleep.
4. The TUI intercepts the worker's obituary, discreetly prints *"worker X is done"* in your active chat, and takes the opportunity to instantly purge the RAM consumed by that worker's history cache.

---

**Summary:** Baifo is a pyramid. At the top, you have a "dumb" but lightning-fast graphical interface (TUI). In the center, a single, highly intelligent event-deduplication protocol (A2A). And at the base, a pure Go concurrency machine controlled by atomic buses and blocking channels, capable of sustaining dozens of LLMs arguing in the backroom without raising your computer's temperature.