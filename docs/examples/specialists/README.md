# Example user-defined specialist: `performance`

This directory ships a complete, ready-to-copy **user-defined specialist** so
you can see the `.json` + `.md` format in one place. It is documentation only —
nothing here is auto-installed into your real config.

## Files

- `performance.json` — the serializable `SpecialistSpec` (kind, inputs, gates,
  lane priority, arbiter policy, witnessability, severity ladder).
- `performance.md` — the system prompt the model runs with.

## Install it

Copy both files into your config's `specialists/` directory:

```sh
mkdir -p ~/.config/appr-ai-sal/specialists
cp docs/examples/specialists/performance.json ~/.config/appr-ai-sal/specialists/
cp docs/examples/specialists/performance.md   ~/.config/appr-ai-sal/specialists/
```

(If you set `APPR_AI_SAL_CONFIG_DIR` or `XDG_CONFIG_HOME`, use that root
instead of `~/.config`.)

On the next review, `performance` runs as a code specialist alongside the
built-in panel: its findings flow through the same deterministic gates,
cross-specialist dedupe, and repo arbiter. Remove the two files to uninstall.

## Write your own

Copy `performance.json` as a starting point and change `name` (it becomes the
lane tag), the prompt, and the fields. Loading is fail-open: a malformed spec
is logged and skipped, and you can never shadow a built-in specialist's name.
See the "User-defined specialists" section of the top-level `README.md` for
the full field reference.
