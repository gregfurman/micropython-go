import argparse
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Optional

# The coverage variant, not standard: it is the only unix build at
# MICROPY_CONFIG_ROM_LEVEL_EVERYTHING, so it is a superset of what this port
# enables. Recording from a reference with fewer features means tests print SKIP
# there while running here, and the snapshot asserts the wrong thing.
#
#   make -C micropython/ports/unix VARIANT=coverage \
#       MICROPY_PY_FFI=0 MICROPY_PY_SSL=0 MICROPY_PY_BTREE=0 \
#       MICROPY_VFS_FAT=0 MICROPY_VFS_LFS1=0 MICROPY_VFS_LFS2=0
DEFAULT_MICROPYTHON = Path("micropython/ports/unix/build-coverage/micropython")


def find_micropython(explicit: Optional[Path]) -> Path:
    for candidate in (explicit, os.environ.get("MICROPYTHON"), DEFAULT_MICROPYTHON):
        if candidate and Path(candidate).exists():
            return Path(candidate).resolve()

    found = shutil.which("micropython")
    if found:
        return Path(found)

    sys.exit(
        "no micropython binary found.\n"
        f"build one with: make -C {DEFAULT_MICROPYTHON.parents[1]} submodules && "
        f"make -C {DEFAULT_MICROPYTHON.parents[1]}\n"
        "or pass --micropython /path/to/micropython, or set MICROPYTHON."
    )

def get_executed_output(mpy: Path, py_file: Path) -> Optional[str]:
    print(f"Executing: {py_file}")
    try:
        driver = 'exec(compile(open(%r).read(), "<string>", "exec"))' % py_file.name
        result = subprocess.run(
            [str(mpy),  "-c", driver],
            # Upstream tests read fixtures beside themselves, so run from the
            # script's own directory and pass a bare name.
            cwd=py_file.parent,
            capture_output=True,
            check=False,
            timeout=10,
        )
    except subprocess.TimeoutExpired:
        print(f"Timeout running {py_file} - skipping.", file=sys.stderr)
        return None
    except Exception as e:
        print(f"Failed to run {py_file}: {e}", file=sys.stderr)
        return None

    if result.returncode != 0:
        first = result.stderr.decode("utf-8", "replace").strip().splitlines()
        reason = first[-1] if first else f"exit {result.returncode}"
        print(f"Skipping {py_file}: micropython failed: {reason}", file=sys.stderr)
        return None

    return result.stdout.decode("utf-8")


def create_snapshot(mpy: Path, target_dir: Path, output_file: Path) -> None:
    snapshot = {}
    this_script = Path(__file__).resolve()

    for py_file in sorted(target_dir.rglob("*.py")):
        if py_file.resolve() == this_script:
            continue
        output = get_executed_output(mpy, py_file)

        if output is not None:
            snapshot[py_file.as_posix()] = {
                "recorded": {
                    # NOTE: These tests rely on stdout, so just hardcode I guess
                    "stdout": output
                }
            }

    output_file.write_text(json.dumps(snapshot, indent=4), encoding="utf-8")
    print(f"\nSnapshot successfully saved to {output_file} ({len(snapshot)} scripts)")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Create a JSON snapshot of MicroPython test outputs."
    )
    parser.add_argument(
        "target",
        nargs="?",
        default=".",
        type=Path,
        help="Target directory to scan for .py files (default: .)",
    )
    parser.add_argument(
        "output",
        nargs="?",
        default="snapshot.json",
        type=Path,
        help="Output JSON file name (default: snapshot.json)",
    )
    parser.add_argument(
        "--micropython",
        type=Path,
        default=None,
        help=f"micropython binary to record with (default: ${{MICROPYTHON}}, {DEFAULT_MICROPYTHON}, then $PATH)",
    )

    args = parser.parse_args()
    mpy = find_micropython(args.micropython)
    print(f"Recording with: {mpy}\n")
    create_snapshot(mpy, args.target, args.output)
