#!/usr/bin/env python3
"""Small regression checks for PWF current-context selection."""
import unittest

import context_frame


class CurrentContextTests(unittest.TestCase):
    def test_marked_progress_prefers_top_current_block(self):
        data = b"## Now <!-- CURRENT -->\nnew state\n## History\nold state\n"
        selected, truncated = context_frame.select_current_context("progress", data, lines=20)
        self.assertIn(b"new state", selected)
        self.assertNotIn(b"old state", selected)
        self.assertTrue(truncated)

    def test_unmarked_progress_keeps_prepend_and_append_conventions(self):
        data = b"top newest\n" + b"middle\n" * 24 + b"bottom legacy\n"
        selected, truncated = context_frame.select_current_context("progress", data, lines=2)
        self.assertIn(b"top newest", selected)
        self.assertIn(b"bottom legacy", selected)
        self.assertTrue(truncated)

    def test_repeated_markers_keep_each_marked_section(self):
        data = (
            b"## One (CURRENT)\nfirst\n## Two\nignored\n"
            b"## Three <!-- CURRENT -->\nsecond\n"
        )
        selected, _ = context_frame.select_current_context("progress", data, lines=1)
        self.assertIn(b"first", selected)
        self.assertIn(b"second", selected)
        self.assertNotIn(b"ignored", selected)

    def test_plan_current_excludes_stale_head_sections(self):
        data = (
            b"# Plan\nintro\n## Work (CURRENT)\nactive\n"
            b"### Detail\nstill active\n## Old redraw\nstale\n"
        )
        selected, _ = context_frame.select_current_context("plan", data, lines=2)
        self.assertIn(b"# Plan", selected)
        self.assertIn(b"active", selected)
        self.assertIn(b"still active", selected)
        self.assertNotIn(b"stale", selected)


if __name__ == "__main__":
    unittest.main()
