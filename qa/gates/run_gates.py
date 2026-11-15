"""CLI entrypoint for running agy-skills House-Law verification gates."""

import argparse
from pathlib import Path
import sys

from . import (
    check_density,
    check_line_cap,
    check_portable,
    check_shape,
    check_vocab,
)

GATES = {
    "portable": check_portable,
    "line-cap": check_line_cap,
    "density": check_density,
    "vocab": check_vocab,
    "shape": check_shape,
}


def run_selected(root, gate_names):
    """Run selected gates and return count of failing gates."""
    failures = 0
    for name in gate_names:
        violations = GATES[name](root)
        if violations:
            failures += 1
            sys.stdout.write(f"FAIL [{name}] ({len(violations)} violation(s)):\n")
            for item in violations:
                sys.stdout.write(f"  - {item}\n")
        else:
            sys.stdout.write(f"PASS [{name}]\n")
    return failures


def main(argv=None):
    """Parse CLI args and run requested QA gates."""
    parser = argparse.ArgumentParser(description="Run agy-skills House-Law gates")
    parser.add_argument(
        "--gate",
        choices=list(GATES.keys()) + ["all"],
        default="all",
        help="Gate to execute (default: all)",
    )
    parser.add_argument(
        "--root",
        default=str(Path(__file__).resolve().parents[2]),
        help="Repository root directory",
    )
    args = parser.parse_args(argv)
    selected = list(GATES.keys()) if args.gate == "all" else [args.gate]
    failures = run_selected(Path(args.root), selected)
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
