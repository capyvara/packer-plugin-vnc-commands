#!/usr/bin/env python3
import json
import re
import subprocess
import sys

metadata = json.loads(subprocess.check_output([sys.argv[1], "describe"]))
assert metadata["provisioners"] == ["-packer-default-plugin-name-"], metadata
assert metadata["builders"] == [] and metadata["post_processors"] == [] and metadata["datasources"] == [], metadata
assert metadata["sdk_version"] == "0.6.12", metadata
assert re.fullmatch(r"x\d+\.\d+", metadata["api_version"]), metadata
assert re.fullmatch(r"\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", metadata["version"]), metadata
print(json.dumps(metadata))
