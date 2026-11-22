#!/usr/bin/env python3
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
PATTERN = (
    "^Test(BucketEncryption|FindLiveVirtualIPAddress|FirstPEMLine$|"
    "FloatingIPHarness|IAMUserHarness|IsLiveVirtualIPName$|"
    "IsVirtualIPAddressDuplicateRefusal$|LegacyWriteRequiresOwnGate$|"
    "LiveSpendCreditSynthetic$|NATHarness|PickEnabledZoneID$|PollOTPFile|"
    "ReadOTPFileOnce$|RegisterVirtualIPCleanupIfCreated$|S3SignerKnownVector$|"
    "SafeErr$|SafeVirtualIPErr|StorageAutoRenew|StorageProject|SystemTagsEqual$|"
    "ValidateLiveVirtualIPAddressDelete)"
)

with tempfile.TemporaryDirectory(prefix="vngcloud-offline-") as temp:
    directory = Path(temp)
    binary = directory / "offline.test"
    build_env = {key: value for key, value in os.environ.items() if key in (
        "PATH", "HOME", "GOCACHE", "GOMODCACHE", "GOPATH", "GOTOOLCHAIN",
        "GOPROXY", "GOSUMDB", "TMPDIR", "CGO_ENABLED", "CC", "SDKROOT",
        "MACOSX_DEPLOYMENT_TARGET",
    )}
    subprocess.run(["go", "test", "-race", "-tags", "livewrite", "-c",
                    "-o", str(binary), "./livetest/"],
                   cwd=ROOT, env=build_env, check=True)
    (directory / "go.mod").write_text("module synthetic.invalid/offline\n")
    shutil.copytree(ROOT / "testdata/storage", directory / "testdata/storage")
    (directory / "livetest").mkdir()
    source = "live_write_storage_project_test.go"
    shutil.copyfile(ROOT / "livetest" / source, directory / "livetest" / source)
    result = subprocess.run([str(binary), "-test.run", PATTERN, "-test.timeout", "5m"],
                            cwd=directory, env={"HOME": temp, "TMPDIR": temp})
    raise SystemExit(result.returncode)
