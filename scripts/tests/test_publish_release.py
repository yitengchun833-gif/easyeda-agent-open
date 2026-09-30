"""Offline checks for release upload safety and interrupted-draft recovery."""

import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


SPEC = importlib.util.spec_from_file_location(
    "publish_release", Path(__file__).resolve().parents[1] / "publish-release.py")
publish = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(publish)


class PublishReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.dist = Path(self.temp.name)
        (self.dist / "release-notes.md").write_text("Reviewed release notes\n")

    def asset(self, name, data):
        path = self.dist / name
        path.write_bytes(data)
        with (self.dist / "checksums.txt").open("a") as stream:
            stream.write(f"{hashlib.sha256(data).hexdigest()}  {name}\n")
        return path

    def test_minor_bundle_is_mandatory_even_when_checksum_omits_it(self):
        self.asset("easyeda_linux_amd64", b"binary")
        with self.assertRaisesRegex(ValueError, "missing test-evidence.zip"):
            publish.checked_assets("v1.6.0", self.dist)
        self.asset("test-evidence.zip", b"reviewed evidence")
        paths, sizes, _ = publish.checked_assets("v1.6.0", self.dist)
        self.assertEqual(set(sizes), {"easyeda_linux_amd64", "test-evidence.zip", "checksums.txt"})
        self.assertIn(self.dist / "test-evidence.zip", paths)
        (self.dist / "test-evidence.zip").unlink()
        with self.assertRaisesRegex(ValueError, "missing or changed"):
            publish.checked_assets("v1.6.0", self.dist)

    def test_remote_draft_missing_bundle_cannot_be_published(self):
        self.asset("test-evidence.zip", b"reviewed evidence")
        _, sizes, digests = publish.checked_assets("v1.6.0", self.dist)
        with self.assertRaisesRegex(ValueError, "missing or has unexpected"):
            publish.check_remote_assets({"isDraft": True, "assets": []}, sizes, digests)

    def test_remote_without_digest_requires_downloaded_sha256(self):
        self.asset("test-evidence.zip", b"reviewed evidence")
        _, sizes, digests = publish.checked_assets("v1.6.0", self.dist)
        assets = [{"name": name, "size": size} for name, size in sizes.items()]
        missing = publish.check_remote_assets({"isDraft": True, "assets": assets}, sizes, digests)
        self.assertEqual(set(missing), set(sizes))

        def wrong_download(*args, **_kwargs):
            Path(args[-1], args[5]).write_bytes(b"wrong content")

        with patch.object(publish, "run", side_effect=wrong_download):
            with self.assertRaisesRegex(ValueError, "downloaded GitHub asset SHA256 differs"):
                publish.verify_downloaded_assets("v1.6.0", missing, digests)

        def exact_download(*args, **_kwargs):
            name = args[5]
            Path(args[-1], name).write_bytes((self.dist / name).read_bytes())

        with patch.object(publish, "run", side_effect=exact_download):
            publish.verify_downloaded_assets("v1.6.0", missing, digests)

    def test_matching_draft_reuploads_then_publishes(self):
        self.asset("test-evidence.zip", b"reviewed evidence")
        _, sizes, digests = publish.checked_assets("v1.6.0", self.dist)
        assets = [{"name": name, "size": size, "digest": f"sha256:{digests[name]}"}
                  for name, size in sizes.items()]
        calls = []

        def fake_run(*args, **_kwargs):
            calls.append(args)

        with patch.object(publish, "release_state", side_effect=[
                {"isDraft": True, "assets": []},
                {"isDraft": True, "assets": assets},
                {"isDraft": False, "assets": assets}]), \
                patch.object(publish, "ensure_tag"), patch.object(publish, "run", side_effect=fake_run):
            publish.publish("v1.6.0", self.dist)
        self.assertTrue(any(args[:3] == ("gh", "release", "upload") for args in calls))
        self.assertTrue(any(args[-1] == "--draft=false" for args in calls))

    def test_published_release_is_never_replaced(self):
        self.asset("test-evidence.zip", b"reviewed evidence")
        with patch.object(publish, "release_state", return_value={"isDraft": False, "assets": []}), \
                patch.object(publish, "ensure_tag") as tag:
            with self.assertRaisesRegex(ValueError, "already published"):
                publish.publish("v1.6.0", self.dist)
            tag.assert_not_called()

    def test_interrupted_draft_upload_never_publishes(self):
        self.asset("test-evidence.zip", b"reviewed evidence")
        calls = []

        def failed_upload(*args, **_kwargs):
            calls.append(args)
            if args[:3] == ("gh", "release", "upload"):
                raise ValueError("upload interrupted")

        with patch.object(publish, "release_state", return_value={"isDraft": True, "assets": []}), \
                patch.object(publish, "ensure_tag"), patch.object(publish, "run", side_effect=failed_upload):
            with self.assertRaisesRegex(ValueError, "upload interrupted"):
                publish.publish("v1.6.0", self.dist)
        self.assertFalse(any("--draft=false" in args for args in calls))


if __name__ == "__main__":
    unittest.main()
