import unittest
from worker import translate

class WorkerTests(unittest.TestCase):
    def test_コードとURLと空白を保持(self):
        source = 'Hello world.\n\n```go\nfmt.Println("hello")\n```\nSee `README.md` and https://example.com/a.\n'
        result = translate(source, 'ja', lambda text, _: text.upper())
        self.assertIn('```go\nfmt.Println("hello")\n```\n', result)
        self.assertIn('`README.md`', result)
        self.assertIn('https://example.com/a.', result)
        self.assertTrue(result.endswith('\n'))
        self.assertIn('HELLO WORLD.', result)
    def test_閉じていないコードとインデントコードを保持(self):
        source = 'Report.\n\n    print("hello")\n\n```python\nprint("world")\n'
        result = translate(source, 'ja', lambda text, _: text.upper())
        self.assertIn('    print("hello")', result)
        self.assertIn('```python\nprint("world")\n', result)
        self.assertIn('REPORT.', result)
    def test_JSONと壊れたプレースホルダを翻訳しない(self):
        source = '{"status":"ok"}'
        self.assertEqual(source, translate(source, 'ja', lambda *_: self.fail()))
        with self.assertRaises(ValueError):
            translate('See `README.md`.', 'ja', lambda *_: 'missing code')
    def test_長文を欠落なく分割(self):
        source = ('hello world\n\n' * 1000) + '終わり'
        chunks = []
        self.assertEqual(source, translate(source, 'en', lambda text, _: chunks.append(text) or text))
        self.assertTrue(all(len(chunk) <= 1800 for chunk in chunks))
if __name__ == '__main__':
    unittest.main()
