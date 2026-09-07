"""Bundle Zig's runtime in the pinned Zova archive for external CGO linkers."""
import argparse
from pathlib import Path


ANCHOR = '    c_abi_lib.root_module.addOptions("zova_build_options", zova_build_options);'
SETTING = "    c_abi_lib.bundle_compiler_rt = true;"


def prepare(build: Path) -> None:
    source = build.read_text(encoding="utf-8")
    if source.count(ANCHOR) != 1:
        raise ValueError("unexpected Zova build layout; review the compiler runtime patch")
    if SETTING in source:
        return
    source = source.replace(ANCHOR, SETTING + "\n" + ANCHOR)
    build.write_text(source, encoding="utf-8", newline="\n")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("build", type=Path)
    args = parser.parse_args()
    try:
        prepare(args.build)
    except (OSError, ValueError) as error:
        parser.error(str(error))


if __name__ == "__main__":
    main()
