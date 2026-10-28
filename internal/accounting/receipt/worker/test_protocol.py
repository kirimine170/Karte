import io
import json
import unittest
from contextlib import redirect_stdout

from protocol import emit, parse_request


class ProtocolTest(unittest.TestCase):
    def test_parse_recognize(self):
        request = parse_request(json.dumps({"request_id":"r1","method":"recognize","params":{"path":"/tmp/a.png","output_dir":"/tmp/out","visualize":True}}))
        self.assertEqual(request.request_id,"r1")
        self.assertEqual(request.document_type,"receipt")

    def test_emit_is_single_json_line(self):
        stream=io.StringIO()
        with redirect_stdout(stream): emit({"type":"ready","message":"日本語"})
        self.assertEqual(len(stream.getvalue().splitlines()),1)
        self.assertEqual(json.loads(stream.getvalue())["type"],"ready")

    def test_parse_regional_refinement(self):
        request = parse_request(json.dumps({"request_id":"r2","method":"recognize","params":{"path":"/tmp/a.png","output_dir":"/tmp/out","region":{"x":0.1,"y":0.2,"width":0.3,"height":0.4},"preprocess_variant":"upscale_contrast"}}))
        self.assertEqual(request.region, (0.1, 0.2, 0.3, 0.4))
        self.assertEqual(request.preprocess_variant, "upscale_contrast")

    def test_reject_unknown_method(self):
        with self.assertRaises(ValueError): parse_request('{"request_id":"r1","method":"bad","params":{}}')


if __name__ == "__main__": unittest.main()
