STABLE_RECEIPT_SCHEMA = "wukongim/stable-package-client-validation/v1"
TEST_PRODUCT_SHA = "fc18221282f289088ec9b4282fda8e0bfc67a3b8"


def stable_client_command(image, downloads, certificate, family, bootstrap):
    """Use the real stable bootstrap's sources and mirrorlist without a baseurl fallback."""
    _require(family in {"apt", "rpm"} and bootstrap is not None,
             "stable client requires its family bootstrap")
    command = _client_command(image, downloads, certificate, family, None,
                              COMPOSER.REPOSITORY_ORIGIN, bootstrap)
    script = command[-1].replace("preview", "stable")
    if family == "apt":
        script = script.replace("wukongim-archive-keyring", "wukongim-archive-keyring-stable")
    else:
        script = script.replace("wukongim-release", "wukongim-release-stable")
        start = script.index('  if ! grep -Fxq "baseurl=$WK_REPOSITORY_URL"')
        end = script.index("\ndnf_options=(", start)
        script = (script[:start] +
                  '  grep -Fxq "mirrorlist=$WK_REPOSITORY_URL" /tmp/repos.d/wukongim-stable.repo\n'
                  'else\n  echo "stable bootstrap is required" >&2\n  exit 1\nfi' + script[end:])
        script = script.replace("baseurl=https://packages.githubim.com/rpm/stable/el/9/x86_64",
                                "mirrorlist=https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist")
        # A CA override is supplied only by the isolated TEST ONLY runner. Normal
        # HTTPS clients use the image's ordinary trust store and keep sslverify=1.
        script = script.replace('dnf "${dnf_options[@]}" makecache',
                                'if [[ -n "${WK_TEST_CA_BUNDLE:-}" ]]; then\n'
                                '  test "${WK_TEST_ONLY_CLIENT:-}" = true\n'
                                '  dnf_options+=(--setopt=wukongim-stable.sslcacert="$WK_TEST_CA_BUNDLE")\n'
                                'fi\ndnf "${dnf_options[@]}" makecache')
    script = 'test -n "${WK_BOOTSTRAP_PACKAGE:-}"\n' + script
    for i, value in enumerate(command[:-1]):
        if value.endswith(f":/keys/{family}-preview.asc:ro"):
            command[i] = value.replace(f":/keys/{family}-preview.asc:ro", f":/keys/{family}-stable.asc:ro")
        if value.startswith("WK_REPOSITORY_URL=") and family == "rpm":
            command[i] = "WK_REPOSITORY_URL=https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist"
    command[-1] = script
    return command


def _validate_stable_clients(*, site_root, snapshot_path, base_url, transport_inputs,
                             apt_public_cert, rpm_public_cert, expected_version,
                             verify_installed_cli, test_only, test_product_sha, runner, fetcher):
    """Authenticate explicit stable inputs, then exercise the four actual package clients."""
    _require(transport_inputs is not None, "stable requires transport inputs")
    _require(site_root is None and snapshot_path is None,
             "stable derives its snapshot from transport inputs")
    _require(base_url == COMPOSER.REPOSITORY_ORIGIN,
             "stable clients require the canonical HTTPS origin")
    _require(isinstance(expected_version, str) and RELEASE_VERSION_RE.fullmatch(expected_version),
             "stable target must be strict release SemVer")
    _require(test_only or test_product_sha is None,
             "TEST ONLY product identity requires explicit test mode")
    _require(not test_only or test_product_sha == TEST_PRODUCT_SHA,
             "TEST ONLY clients require the fixed fixture product SHA")
    tools = ("gpg", "gpgv", "rpm", "rpmkeys", "dpkg-deb", "zstd")
    missing = [name for name in tools if shutil.which(name) is None]
    _require(not missing, "stable native verifier tools are unavailable: " + ",".join(missing))
    planner = COMPOSER.local_module("plan-object-storage-publication.py")
    try:
        input_bytes = _read_regular(transport_inputs, "transport inputs", MAX_CONTROL_BYTES)
        inputs = planner.composite_inputs(transport_inputs, test_only)
        planned = planner.make_composite_plan(transport_inputs, test_only)
        _require(_read_regular(transport_inputs, "transport inputs", MAX_CONTROL_BYTES) == input_bytes,
                 "transport inputs changed during verification")
        descriptor = inputs["current"]["stable"]
        identity = next(item for item in planned["snapshots"]
                        if item["channel"] == "stable" and item["audit_release_id"] == descriptor["audit_release_id"])
        _require(identity["candidate"] == descriptor["candidate"], "stable candidate differs from transport input")
        candidate = identity["candidate"]
        _require(candidate["version"] == expected_version, "stable candidate differs from exact target")
        source_sha = test_product_sha if test_only else candidate["source"]["source_sha"]
        _require(isinstance(source_sha, str) and SHA1_RE.fullmatch(source_sha),
                 "stable target source SHA is invalid")
        archive = planner.local_path(descriptor["archive"], transport_inputs.parent, "stable archive")
        prefix = f"snapshots/stable/r{identity['audit_release_id']}-{candidate['snapshot_sha256']}"
        mirror_path = "/rpm/stable/el/9/x86_64/mirrorlist"
        mirror_route = next(item for item in planned["entrypoints"] if item["public_uri"] == mirror_path)
        mirror = next(item for item in planned["generated_objects"]
                      if item["object_key"] == mirror_route["object_key"])["content_utf8"].encode()
        origin = COMPOSER.REPOSITORY_ORIGIN
        _require(mirror == (origin + "/" + prefix + "/site/rpm/stable/el/9/x86_64/\n").encode(),
                 "stable mirrorlist differs from candidate snapshot")
        with tempfile.TemporaryDirectory(prefix="wk-stable-clients-") as temporary:
            root = Path(temporary)
            extracted = root / "snapshot"
            extracted_facts = planner.R.snapshot_archive.extract_snapshot(
                archive_path=archive, output_dir=extracted,
                max_members=planner.R.snapshot_archive.DEFAULT_MAX_MEMBERS,
                max_total_size=planner.R.ARCHIVE_MAX_TOTAL_BYTES)
            _require(extracted_facts["sha256"] == candidate["archive"]["sha256"]
                     and extracted_facts["size"] == candidate["archive"]["size"],
                     "stable archive changed during client preparation")
            snapshot_bytes = _read_regular(extracted / "audit/snapshot.json", "stable snapshot", MAX_CONTROL_BYTES)
            _require(_sha256(snapshot_bytes) == candidate["snapshot_sha256"], "stable snapshot digest differs")
            snapshot = json.loads(snapshot_bytes, object_pairs_hook=_reject_duplicate_pairs)
            bootstrap_bytes = _read_regular(extracted / "site/bootstrap/manifest.json", "stable bootstrap", MAX_CONTROL_BYTES)
            bootstrap = COMPOSER.validate_bootstrap_inventory(
                json.loads(bootstrap_bytes, object_pairs_hook=_reject_duplicate_pairs), prepared=False, channel="stable")
            certs = {"apt": apt_public_cert, "rpm": rpm_public_cert}
            for family, path in certs.items():
                public = _read_regular(path, family + " public certificate", MAX_CERTIFICATE_BYTES)
                archived = _read_regular(extracted / f"site/keys/{family}-stable.asc", "archived certificate", MAX_CERTIFICATE_BYTES)
                _require(public == archived, "stable reviewed public certificate differs")
                _require(fetcher(origin + f"/keys/{family}-stable.asc", MAX_CERTIFICATE_BYTES) == public,
                         "stable public endpoint certificate differs")
            _require(fetcher(origin + "/" + prefix + "/site/bootstrap/manifest.json", MAX_CONTROL_BYTES) == bootstrap_bytes,
                     "stable public bootstrap inventory differs")
            inrelease = _read_regular(extracted / "site/apt/dists/stable/InRelease", "stable InRelease", MAX_CONTROL_BYTES)
            _require(fetcher(origin + "/apt/dists/stable/InRelease", MAX_CONTROL_BYTES) == inrelease,
                     "stable public InRelease differs from exact target")
            _require(fetcher(origin + mirror_path, MAX_CONTROL_BYTES) == mirror,
                     "stable public mirrorlist differs from exact target")
            results = {"apt": [], "rpm": []}
            for family, clients in (("apt", APT_CLIENTS), ("rpm", RPM_CLIENTS)):
                package = bootstrap["packages"][family]
                raw = _read_regular(extracted / "site" / package["repository_path"], "stable bootstrap package", MAX_BOOTSTRAP_BYTES)
                _require(len(raw) == package["published_size"] and _sha256(raw) == package["published_sha256"],
                         "stable bootstrap package differs from inventory")
                for suffix in (package["download_path"], prefix + "/site/" + package["repository_path"]):
                    _require(fetcher(origin + "/" + suffix, MAX_BOOTSTRAP_BYTES) == raw,
                             "stable public bootstrap package differs")
                expected = {item["published_sha256"]: [item["version"]]
                            for item in snapshot["payloads"][family] if item["indexed"]}
                _require(len(expected) == 1 and next(iter(expected.values())) == [expected_version],
                         "stable indexed target differs")
                for distribution, image in clients:
                    downloads = root / distribution
                    downloads.mkdir(mode=0o777); downloads.chmod(0o777)
                    command = stable_client_command(image, downloads, certs[family], family,
                                                    extracted / "site" / package["repository_path"])
                    runner(command)
                    downloaded = _download_receipt(downloads, family, expected)
                    _require(downloaded["snapshot_versions"] == [expected_version], "stable downloaded target differs")
                    installed = None
                    if verify_installed_cli:
                        evidence = root / ("installed-" + distribution)
                        evidence.mkdir(mode=0o777); evidence.chmod(0o777)
                        expected_identity = {"version": expected_version, "commit": source_sha, "build_source": "release"}
                        runner(_installed_cli_command(image, downloads / downloaded["filename"], evidence,
                                                      certs[family], family, expected_identity, downloaded["sha256"]))
                        installed = _installed_cli_receipt(evidence / "installed-cli.json", expected_identity)
                    results[family].append({"distribution": distribution, "image": image,
                                            "bootstrap_installed": True, "download": downloaded, "installed_cli": installed})
            _require(fetcher(origin + "/apt/dists/stable/InRelease", MAX_CONTROL_BYTES) == inrelease
                     and fetcher(origin + mirror_path, MAX_CONTROL_BYTES) == mirror,
                     "stable public entrypoints changed during client validation")
        _require(_read_regular(transport_inputs, "transport inputs", MAX_CONTROL_BYTES) == input_bytes,
                 "transport inputs changed during client validation")
        return {"schema": STABLE_RECEIPT_SCHEMA, "channel": "stable", "test_only": test_only,
                "base_url": origin, "expected_version": expected_version, "expected_source_sha": source_sha,
                "expected_version_verified": True, "installed_cli_verified": verify_installed_cli,
                "bootstrap_verified": True, "bootstrap_version": bootstrap["version"],
                "repository_entrypoint_executed": False, "audit_release_id": identity["audit_release_id"],
                "snapshot_sha256": candidate["snapshot_sha256"], "transport_inputs_sha256": _sha256(input_bytes),
                "transport_plan_sha256": _sha256(_canonical_json(planned)),
                "status_revalidated": False, "stable_entrypoints_revalidated": True,
                "github_provenance_verified": False, "publication_verified": False,
                "native_verifier_tools": {name: shutil.which(name) for name in tools}, **results}
    except ClientValidationError:
        raise
    except (RuntimeError, ValueError, OSError, KeyError, TypeError, StopIteration, subprocess.SubprocessError) as error:
        raise ClientValidationError("stable client verification failed: " + str(error)) from error


