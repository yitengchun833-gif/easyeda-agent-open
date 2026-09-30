import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('gen', Path(__file__).with_name('gen.py'))
gen = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gen)


class PublicSdkDeclarations(unittest.TestCase):
    def test_public_and_legacy_members(self):
        for prefix in ['', 'public ']:
            self.assertEqual(gen.METHOD_RE.match(f'  {prefix}create(x: number): Promise<void>;').group(1), 'create')
            self.assertEqual(gen.EDA_PROP_RE.match(f'  {prefix}dmt_Board: DMT_Board;').groups(), ('dmt_Board', 'DMT_Board'))
        self.assertIsNone(gen.METHOD_RE.match('private secret(): void;'))


if __name__ == '__main__':
    unittest.main()
