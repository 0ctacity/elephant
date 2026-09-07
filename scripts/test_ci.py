"""Regression checks for cross-platform release preparation."""
from pathlib import Path
import subprocess
import tempfile
import unittest

from prepare_zova import prepare

ROOT = Path(__file__).resolve().parents[1]


class CITests(unittest.TestCase):
    def test_compiler_runtime_is_bundled_once(self):
        with tempfile.TemporaryDirectory() as directory:
            build = Path(directory) / "build.zig"
            build.write_text('    c_abi_lib.root_module.addOptions("zova_build_options", zova_build_options);\n')
            prepare(build)
            prepare(build)
            self.assertEqual(build.read_text().count("c_abi_lib.bundle_compiler_rt = true;"), 1)

    def test_unknown_zova_layout_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            build = Path(directory) / "build.zig"
            build.write_text("unexpected build layout\n")
            with self.assertRaises(ValueError):
                prepare(build)
            self.assertEqual(build.read_text(), "unexpected build layout\n")

    def test_windows_checkout_preserves_lf(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "init", "-q", directory], check=True)
            (root / ".gitattributes").write_bytes((ROOT / ".gitattributes").read_bytes())
            content = b"package example\n\nfunc Example() {}\n"
            (root / "example.go").write_bytes(content)
            subprocess.run(["git", "-C", directory, "-c", "core.autocrlf=true", "add", "."], check=True, capture_output=True)
            (root / "example.go").unlink()
            subprocess.run(["git", "-C", directory, "-c", "core.autocrlf=true", "checkout-index", "-f", "example.go"], check=True)
            self.assertEqual((root / "example.go").read_bytes(), content)


if __name__ == "__main__":
    unittest.main()
