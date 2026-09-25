# quietbench

quietbench runs a benchmark on an Apple Silicon Mac under the steadiest conditions macOS allows,
and tells you afterwards whether they held.

macOS doesn't let you pin CPU frequency or core affinity,
so a tool like [perflock](https://github.com/aclements/perflock) isn't possible.
quietbench controls what it can instead.

Before the command starts, quietbench:

- Refuses to run on battery.
- Waits until the CPU has been idle and thermal pressure nominal for a while (30 seconds by default).
- Sets `GOMAXPROCS` to the number of performance cores, unless you set it yourself.
- Optionally turns on Low Power Mode, which caps clock speeds so they drift less as the chip heats up.

While the command runs, quietbench keeps the Mac from sleeping and watches thermal pressure.
Afterwards it reports the peak thermal pressure and how much CPU other processes used.

## Install

```sh
go install github.com/mapno/quietbench@latest
```

## Usage

```sh
quietbench [flags] command [args...]
quietbench                                  # print the current conditions
```

For example:

```sh
quietbench go test -run '^$' -bench . -count 10 ./pkg/foo > new.txt
```

| Flag | Default | Meaning |
|---|---|---|
| `-idle` | `95` | Percent of CPU time, across all cores, that must be idle before starting |
| `-settle` | `30s` | How long the machine must stay idle at nominal thermal pressure before starting |
| `-timeout` | `5m` | Give up if the machine hasn't settled by then; `0` waits forever |
| `-lowpower` | off | Turn on Low Power Mode for the run and restore it afterwards; runs `sudo pmset` |

quietbench exits with the command's exit code.
Its own messages go to stderr, so the command's stdout is left untouched.

## Low Power Mode without a password prompt

`-lowpower` runs `sudo pmset -c lowpowermode 1` before the command and restores the setting afterwards.
If the command outlasts sudo's timeout, restoring the setting prompts for a password again.
To avoid that, allow the command without a password using `sudo visudo -f /etc/sudoers.d/quietbench`:

```
<your-user> ALL=(root) NOPASSWD: /usr/bin/pmset -c lowpowermode 0, /usr/bin/pmset -c lowpowermode 1
```
