import argparse
import json
import subprocess
import sys
from pathlib import Path
from typing import Optional

def get_file_output(py_file: Path) -> Optional[str]:
    for ext in (f"{py_file.name}.exp", py_file.with_suffix(".exp").name):
        exp_path = py_file.with_name(ext)
        if exp_path.exists():
            print(f"Reading .exp file for: {py_file}")
            try:
                # newline="" disables universal newline translation to preserve raw \r
                return exp_path.read_text(encoding="utf-8", newline="")
            except Exception as e:
                print(f"Failed to read {exp_path}: {e}", file=sys.stderr)
                return None
    return None

def get_executed_output(py_file: Path) -> Optional[str]:
    print(f"Executing: {py_file}")
    try:
        result = subprocess.run(
            [sys.executable, str(py_file)],
            capture_output=True,
            check=False,
            timeout=10  # Prevent infinite loops
        )
        return result.stdout.decode("utf-8")
    except subprocess.TimeoutExpired:
        print(f"Timeout running {py_file} - skipping.", file=sys.stderr)
    except Exception as e:
        print(f"Failed to run {py_file}: {e}", file=sys.stderr)
        
    return None

def create_snapshot(target_dir: Path, output_file: Path) -> None:
    snapshot = {}
    this_script = Path(__file__).resolve()

    for py_file in target_dir.rglob("*.py"):
        if py_file.resolve() == this_script:
            continue
            
        output = get_file_output(py_file)
        if output is None:
            output = get_executed_output(py_file)

        if output is not None:
            snapshot[py_file.as_posix()] = {
                "recorded": {
                    # NOTE: These tests rely on stdout, so just hardcode I guess
                    "stdout": output
                }
            }

    output_file.write_text(json.dumps(snapshot, indent=4), encoding="utf-8")
    print(f"\nSnapshot successfully saved to {output_file}")

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Create a JSON snapshot of Python test outputs.")
    parser.add_argument(
        "target", 
        nargs="?", 
        default=".", 
        type=Path, 
        help="Target directory to scan for .py files (default: .)"
    )
    parser.add_argument(
        "output", 
        nargs="?", 
        default="snapshot.json", 
        type=Path, 
        help="Output JSON file name (default: snapshot.json)"
    )
    
    args = parser.parse_args()
    create_snapshot(args.target, args.output)