#!/usr/bin/env python3
import sys
import json
import base64

def main():
    try:
        input_data = json.load(sys.stdin)
        filepath = input_data.get('filepath', 'unknown')
        mime_type = input_data.get('mime_type', '')
        # bytes are base64 encoded by json.Marshal in Go for []byte
        content_b64 = input_data.get('bytes', '')
        
        # Simple reference implementation: return filename and size
        content_len = 0
        if content_b64:
            content_len = len(base64.b64decode(content_b64))

        result = {
            "text": f"Python Plugin: Processed {filepath}",
            "metadata": {
                "plugin": "reference-python",
                "original_mime": mime_type,
                "content_length": content_len
            }
        }
        print(json.dumps(result))
    except Exception as e:
        print(json.dumps({"error": str(e)}))
        sys.exit(1)

if __name__ == "__main__":
    main()
