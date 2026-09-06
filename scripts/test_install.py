"""Exercise the shell installer offline with real release archives."""
import hashlib
import io
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile

SCRIPT = Path(__file__).resolve().parents[1] / "install.sh"


@unittest.skipIf(os.name == "nt", "POSIX shell integration runs on Linux and macOS")
class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "tools"
        self.bin.mkdir()
        self.dest = self.root / "installed bin"
        self.fixtures = self.root / "fixtures"
        self.fixtures.mkdir()
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                        FIXTURES=str(self.fixtures), FAKE_OS="Linux", FAKE_ARCH="x86_64")
        self.tool("uname", 'import os,sys\nprint(os.environ["FAKE_OS" if sys.argv[1]=="-s" else "FAKE_ARCH"])')
        self.tool("curl", '''import os,sys,shutil
from pathlib import Path
args=sys.argv[1:]
if args[-1].endswith('/releases/latest'):
 print(args[-1].removesuffix('/latest')+'/tag/v1.2.3',end='')
else:
 source=Path(os.environ['FIXTURES'])/args[-1].rsplit('/',1)[-1]
 if not source.exists(): sys.exit(22)
 shutil.copyfile(source,args[args.index('-o')+1])
''')

    def tool(self, name, source):
        path = self.bin / name
        path.write_text(f"#!{sys.executable}\n{source}\n")
        path.chmod(0o755)

    def archive(self, target="linux-amd64", version="v1.2.3", corrupt=False):
        name = f"elephant-{version}-{target}." + ("zip" if target.startswith("windows") else "tar.gz")
        path = self.fixtures / name
        payload = b"#!/bin/sh\necho elephant fixture\n"
        if target.startswith("windows"):
            with zipfile.ZipFile(path, "w") as archive:
                archive.writestr("elephant.exe", payload)
        else:
            with tarfile.open(path, "w:gz") as archive:
                entry = tarfile.TarInfo("elephant")
                entry.size = len(payload)
                archive.addfile(entry, io.BytesIO(payload))
        checksum = hashlib.sha256(path.read_bytes()).hexdigest() if not corrupt else "0" * 64
        (self.fixtures / "SHA256SUMS.txt").write_text(f"{checksum}  {name}\n")
        return payload

    def run_installer(self, *args):
        return subprocess.run(["sh", str(SCRIPT), "--repo", "test/elephant", "--dir", str(self.dest), *args],
                              env=self.env, text=True, capture_output=True, timeout=15)

    def test_supported_targets(self):
        for system, arch, target in [("Linux", "x86_64", "linux-amd64"), ("Linux", "aarch64", "linux-arm64"),
                                     ("Darwin", "arm64", "macos-arm64"), ("MINGW64_NT-10.0", "x86_64", "windows-amd64")]:
            with self.subTest(target=target):
                self.env.update(FAKE_OS=system, FAKE_ARCH=arch)
                payload = self.archive(target)
                result = self.run_installer()
                self.assertEqual(result.returncode, 0, result.stderr)
                binary = self.dest / ("elephant.exe" if target.startswith("windows") else "elephant")
                self.assertEqual(binary.read_bytes(), payload)
                self.assertTrue(os.access(binary, os.X_OK))

    def test_prerelease(self):
        self.archive(version="v1.0.0-rc.2")
        result = self.run_installer("--version", "1.0.0-rc.2")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_bad_checksum_preserves_existing_binary(self):
        self.dest.mkdir()
        binary = self.dest / "elephant"
        binary.write_text("existing")
        self.archive(corrupt=True)
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum", result.stderr.lower())
        self.assertEqual(binary.read_text(), "existing")

    def test_unsupported_platform(self):
        self.env.update(FAKE_OS="Darwin", FAKE_ARCH="x86_64")
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unsupported", result.stderr.lower())

    def test_bad_version(self):
        result = self.run_installer("--version", "../../bad")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("version", result.stderr.lower())

    def test_piped_script_uses_default_repository(self):
        self.archive()
        result = subprocess.run(["sh", "-s", "--", "--dir", str(self.dest)],
                                input=SCRIPT.read_text(), env=self.env, text=True,
                                capture_output=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.dest / "elephant").is_file())

    def test_missing_checksum_rejected(self):
        self.archive()
        (self.fixtures / "SHA256SUMS.txt").write_text("")
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum", result.stderr.lower())
        self.assertFalse((self.dest / "elephant").exists())

    def test_missing_asset(self):
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.dest / "elephant").exists())


if __name__ == "__main__":
    unittest.main()
