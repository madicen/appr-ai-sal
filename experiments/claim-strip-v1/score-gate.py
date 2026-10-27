#!/usr/bin/env python3
"""Score claim-strip-v1 corpus reps against frozen control baselines + Phase 5 gate."""
from __future__ import annotations

import json
import math
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BASE = ROOT / "docs" / "evals" / "baselines"
TREAT = ROOT / "reports" / "treatments" / "claim-strip-v1"
OUT = ROOT / "docs" / "evals" / "treatments" / "claim-strip-v1-ctx32768-balanced-v1.json"

MODELS = [
    ("qwen2.5-coder:3b", "qwen2.5-coder-3b"),
    ("qwen2.5-coder:7b", "qwen2.5-coder-7b"),
    ("qwen2.5-coder:14b", "qwen2.5-coder-14b"),
]


def rate(num: int, den: int) -> float | None:
    return None if den <= 0 else num / den


def mean(xs: list[float | None]) -> float | None:
    ys = [x for x in xs if x is not None]
    return sum(ys) / len(ys) if ys else None


def main() -> int:
    models_out = []
    overall = "measured-helpful"
    for model, slug in MODELS:
        bpath = BASE / f"{slug}-ctx32768-balanced-v1.json"
        base = json.loads(bpath.read_text())
        b_claim = base["means"]["unsupported_claim_rate"]
        b_brk = base["means"]["suggestion_breakage_rate"]
        b_rec = base["means"]["recall_original_12"]
        b_tok = base["means"]["token_count"]

        reps = []
        deltas = []
        for r in (1, 2, 3):
            p = TREAT / f"{slug}-r{r}.json"
            if not p.is_file():
                print(f"missing {p}", file=sys.stderr)
                return 1
            m = json.loads(p.read_text())
            claim = rate(m["unsupported_claim_num"], m["unsupported_claim_den"])
            brk = rate(m["suggestion_breakage_num"], m["suggestion_breakage_den"])
            rec = rate(m["recall_original_12_num"], m["recall_original_12_den"])
            tok = m["input_tokens"] + m["output_tokens"]
            reps.append(
                {
                    "rep": r,
                    "unsupported_claim": claim,
                    "suggestion_breakage": brk,
                    "recall_original_12": rec,
                    "token_count": tok,
                }
            )
            if claim is not None and b_claim is not None:
                deltas.append(b_claim - claim)  # positive = treatment better

        t_claim = mean([r["unsupported_claim"] for r in reps])
        t_brk = mean([r["suggestion_breakage"] for r in reps])
        t_rec = mean([r["recall_original_12"] for r in reps])
        t_tok = sum(r["token_count"] for r in reps) / 3

        red_pp = None if t_claim is None or b_claim is None else (b_claim - t_claim) * 100
        # lazy CI: all available deltas same sign and none zero
        ci_ok = bool(deltas) and all(d > 0 for d in deltas)
        brk_ok = True
        if t_brk is not None and b_brk is not None:
            brk_ok = (t_brk - b_brk) * 100 <= 0 + 1e-9
        elif t_brk is not None and b_brk is None:
            brk_ok = True
        rec_reg = None if t_rec is None or b_rec is None else (b_rec - t_rec) * 100
        # positive reg = treatment worse (recall fell)
        rec_ok = rec_reg is None or rec_reg <= 2 + 1e-9
        tok_infl = (t_tok / b_tok - 1) * 100 if b_tok else 0
        tok_ok = tok_infl <= 25 + 1e-9
        claim_ok = red_pp is not None and red_pp >= 10 and ci_ok

        outcome = "measured-helpful" if (claim_ok and brk_ok and rec_ok and tok_ok) else "measured-no-uplift"
        if outcome != "measured-helpful":
            overall = "measured-no-uplift"

        models_out.append(
            {
                "model": model,
                "treatment": "claim-strip-v1",
                "baseline": str(bpath.relative_to(ROOT)),
                "baseline_mean_unsupported_claim_rate": b_claim,
                "predicates": {
                    "unsupported_claim_reduction_pp": {
                        "value": red_pp,
                        "need_ge": 10,
                        "pass": bool(red_pp is not None and red_pp >= 10),
                    },
                    "ci_excludes_zero_on_claim_delta": {
                        "deltas": deltas,
                        "pass": ci_ok,
                    },
                    "suggestion_breakage_not_up": {
                        "mean_increase_pp": None
                        if t_brk is None or b_brk is None
                        else (t_brk - b_brk) * 100,
                        "pass": brk_ok,
                    },
                    "recall_regression_pp": {
                        "value": rec_reg,
                        "max": 2,
                        "pass": rec_ok,
                    },
                    "token_inflation_pct": {
                        "value": tok_infl,
                        "max": 25,
                        "pass": tok_ok,
                    },
                },
                "outcome": outcome,
                "treatment_reps": reps,
            }
        )

    snap = {
        "schema_version": 1,
        "treatment": "claim-strip-v1",
        "gate": "docs/evals/plugin-treatment-gate.json",
        "recorded_at": "2026-10-25",
        "overall_outcome": overall,
        "models": models_out,
    }
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(snap, indent=2) + "\n")
    print(f"wrote {OUT} overall={overall}")
    for m in models_out:
        print(f"  {m['model']}: {m['outcome']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
