---
title: Sandbox the agent
description: OS-enforced confinement for the terminal tool.
---

`--sandbox` confines the `terminal` tool to the project directory plus temp, with no network,
enforced by the OS — macOS Seatbelt, Linux Landlock + seccomp. It's off by default and **fails
closed** when the OS mechanism is unavailable, rather than silently running unconfined.

```bash
octo --sandbox                              # confine, deny network
octo --sandbox --sandbox-allow-net          # allow network
octo --sandbox --sandbox-write ./build      # extra writable dir (repeatable)
octo --sandbox --sandbox-read /opt/data     # extra readable dir (repeatable)
```

## Platform support

Linux and macOS are first-class. **`--sandbox` is unavailable on Windows** — OS confinement is
Seatbelt/Landlock only, so on Windows `--sandbox` refuses to run rather than pretend to confine
anything. The permission engine (interactive prompts before each tool call) is the safety layer
there instead.

Next: pair sandboxing with [hooks](/docs/guides/hooks/) for a fully automated, still-confined loop,
or read the full boundary in the [security model](/docs/reference/security/).
