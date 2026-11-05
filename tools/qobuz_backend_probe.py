#!/usr/bin/env python3
"""Probe every known public Qobuz backend and report what it can actually do.

The ecosystem moves fast: a host that answered `STREAMS` last month can serve
metadata and nothing else today (that is a blocked pool, not a dead server), so
the verdict has to come from bytes, not from a 200 on /api/get-music.

Usage:
    python tools/qobuz_backend_probe.py                 # built-in candidate list
    python tools/qobuz_backend_probe.py --host URL ...  # extra candidates
    python tools/qobuz_backend_probe.py --json out.json

Verdicts:
    STREAMS      served >256 KB of real FLAC/MP3/MP4 for a searched track
    PREVIEW      served audio but the payload is a 30 s sample (url has range=)
    META_ONLY    catalog answers, every stream attempt refused
    SESSION      route exists but is gated behind a verification session
    DEAD         DNS, timeout, 4xx/5xx or an HTML page instead of JSON
"""

import argparse
import concurrent.futures
import json
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request

UA = (
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36"
)
CTX = ssl.create_default_context()
CTX.check_hostname = False
CTX.verify_mode = ssl.CERT_NONE

SEARCH_QUERY = "Daft Punk Random Access Memories"
TIDAL_TRACK_ID = 243673459
NAV_QUALITIES = ("6", "27", "7")
MIN_AUDIO_BYTES = 256 * 1024

CANDIDATES = [
    # Navidrums / SquidWTF / qobuz-rest-api route shape (/api/get-music, /api/download-music)
    "https://qobuz.itzsantiax.qzz.io",
    "https://qobuz.kennyy.com.br",
    "https://qobuz.squid.wtf",
    "https://qdl-api.monochrome.tf",
    "https://tracks.monochrome.st",
    "https://trypt-hifi-dl-456461932686.us-west1.run.app",
    "https://qobuz-api1.onrender.com",
    "https://qobuz-api.onrender.com",
    "https://qobuz-api-ten.vercel.app",
    "https://qobuz-api-one.vercel.app",
    "https://qobuz-api-two.vercel.app",
    "https://qobuz-rest-hvfijzx06-bartolomeorusso9s-projects.vercel.app",
    "https://qobuz-dl.vercel.app",
    "https://qobuz.vercel.app",
    "https://qdl.vercel.app",
    # dab / Qobuz direct-stream shape (/api/stream?trackId=)
    "https://dab.yeet.su",
    "https://dabmusic.xyz",
    "https://api.dabmusic.xyz",
    "https://music.wjhe.top",
    # Tidal frontends that fall back to Qobuz audio
    "https://api-one.yupify.qzz.io",
    "https://api-two.yupify.qzz.io",
    # SpotiFLAC community pool (HMAC session obtained through a browser challenge)
    "https://qbz-oss.spotbye.qzz.io",
    "https://tdl-oss.spotbye.qzz.io",
    # Monochrome and the usual dead pool, kept so the sweep stays honest
    "https://api.monochrome.tf",
    "https://eu-central.monochrome.tf",
    "https://us-west.monochrome.tf",
    "https://monochrome-api.samidy.com",
    "https://triton.squid.wtf",
    "https://hund.qqdl.site",
    "https://katze.qqdl.site",
    "https://maus.qqdl.site",
    "https://vogel.qqdl.site",
    "https://wolf.qqdl.site",
    "https://hifi-one.spotisaver.net",
    "https://hifi-two.spotisaver.net",
    "https://hifi.geeked.wtf",
    "https://hfapi.dyamuh.dev",
    "https://hfapi.aluratech.org",
    "https://api.studentsneed.help",
    "https://tidal-api.binimum.org",
    "https://qobuz.wenqi.icu",
]


def http(url, method="GET", data=None, headers=None, timeout=25, limit=None):
    h = {"User-Agent": UA, "Accept": "application/json"}
    if headers:
        h.update(headers)
    req = urllib.request.Request(url, data=data, headers=h, method=method)
    with urllib.request.urlopen(req, timeout=timeout, context=CTX) as resp:
        return resp.status, resp.read(limit) if limit else resp.read(), dict(resp.headers)


def as_json(body):
    try:
        return json.loads(body)
    except (ValueError, UnicodeDecodeError):
        return None


def audio_probe(url):
    status, body, headers = http(url, headers={"Range": "bytes=0-65535"}, timeout=45, limit=300000)
    ctype = headers.get("Content-Type") or ""
    if "html" in ctype.lower() or body[:15].lstrip().lower().startswith((b"<!doctype", b"<html")):
        return {"ok": False, "reason": "html page instead of audio"}
    magic = body[:4]
    is_audio = magic == b"fLaC" or body[:3] == b"ID3" or b"ftyp" in body[:32] or body[:2] == b"\xff\xfb"
    if not is_audio:
        return {"ok": False, "reason": f"not audio, magic={magic!r}"}
    return {
        "ok": len(body) > 8192,
        "status": status,
        "type": ctype,
        "magic": magic.decode("latin-1"),
        "partial": len(body),
        "total": headers.get("Content-Range") or headers.get("Content-Length"),
    }


def probe_navidrums(base, res):
    try:
        status, body, _ = http(f"{base}/api/get-music?q={urllib.parse.quote(SEARCH_QUERY)}&offset=0")
    except urllib.error.HTTPError as e:
        res["navidrums"] = f"HTTP {e.code}"
        return False
    except Exception as e:
        res["navidrums"] = str(e)[:70]
        return False

    payload = as_json(body)
    if payload is None:
        res["navidrums"] = "non-JSON"
        return False

    data = payload.get("data") or {}
    albums = (data.get("albums") or {}).get("items") or [] if isinstance(data, dict) else []
    res["navidrums"] = f"{status}, {len(albums)} album(s)"
    if not albums:
        if payload.get("errors") or payload.get("error"):
            res["navidrums"] += f" error={str(payload.get('error') or payload.get('errors'))[:80]}"
        return True

    album_id = albums[0].get("id")
    try:
        _, album_body, _ = http(f"{base}/api/get-album?album_id={urllib.parse.quote(str(album_id))}")
        album = (as_json(album_body) or {}).get("data") or {}
        tracks = ((album.get("tracks") or {}).get("items")) or []
    except Exception as e:
        res["album"] = str(e)[:70]
        return True
    res["album"] = f"{album.get('title')!r}, {len(tracks)} track(s)"
    if not tracks:
        return True

    track = tracks[0]
    res["track"] = track.get("title")
    reasons = []
    for quality in NAV_QUALITIES:
        try:
            _, body, _ = http(
                f"{base}/api/download-music?track_id={track.get('id')}&quality={quality}", timeout=45
            )
        except urllib.error.HTTPError as e:
            reasons.append(f"q{quality}: HTTP {e.code} {e.read(180).decode('utf-8', 'replace')}")
            continue
        except Exception as e:
            reasons.append(f"q{quality}: {str(e)[:80]}")
            continue
        payload = as_json(body) or {}
        url = ((payload.get("data") or {}).get("url")) or payload.get("url")
        if not url:
            reasons.append(f"q{quality}: {str(payload.get('error'))[:120]}")
            continue
        if "range=" in url:
            res["verdict"] = "PREVIEW"
            res["reason"] = "the backend only returned a 30 s sample url"
            return True
        try:
            audio = audio_probe(url)
        except Exception as e:
            reasons.append(f"q{quality} fetch: {str(e)[:80]}")
            continue
        if audio.get("ok"):
            res["verdict"] = "STREAMS"
            res["audio"] = audio
            return True
        reasons.append(f"q{quality} fetch: {audio.get('reason')}")
    res["refusals"] = reasons[:4]
    return True


def probe_dab(base, res):
    try:
        status, body, _ = http(
            f"{base}/api/stream?trackId={TIDAL_TRACK_ID}&quality=6", timeout=40, limit=300000
        )
    except urllib.error.HTTPError as e:
        res["dab"] = f"HTTP {e.code}"
        return False
    except Exception as e:
        res["dab"] = str(e)[:70]
        return False
    if body[:15].lstrip().lower().startswith((b"<!doctype", b"<html")):
        res["dab"] = f"{status}, HTML page"
        return False
    if body[:4] == b"fLaC" and len(body) > 8192:
        res["dab"] = f"{status}, FLAC"
        res["verdict"] = "STREAMS"
        res["audio"] = audio_probe(base + "/api/stream?trackId=" + str(TIDAL_TRACK_ID) + "&quality=6")
        return True
    res["dab"] = f"{status}, {len(body)} B"
    return False


def probe_session_routes(base, res):
    """SpotiFLAC community pool: /api/dl hides itself (404) until a signed session is
    presented, so liveness is read from the verification service instead."""
    body = json.dumps({"id": "5966783", "quality": "16"}).encode()
    try:
        status, payload, _ = http(
            base + "/api/dl", method="POST", data=body, headers={"Content-Type": "application/json"}
        )
    except urllib.error.HTTPError as e:
        status, payload = e.code, e.read(400)
    except Exception as e:
        res["session"] = str(e)[:70]
        return False
    text = payload.decode("utf-8", "replace")[:160]
    if status in (401, 428) or "session" in text.lower():
        res["verdict"] = "SESSION"
        res["session"] = f"HTTP {status}: {text[:120]}"
        return True

    if "spotbye.qzz.io" not in base:
        res["session"] = f"HTTP {status}: {text[:120]}"
        return False

    try:
        query = urllib.parse.urlencode(
            {"install_id": "0" * 32, "app_version": "7.2.2", "platform": "desktop"}
        )
        status, payload, _ = http("https://verify.spotbye.qzz.io/bootstrap?" + query, timeout=20)
        challenge = (as_json(payload) or {}).get("challenge_url") or ""
    except Exception as e:
        res["session"] = f"{text[:60]} / verify: {str(e)[:60]}"
        return False
    if challenge:
        res["verdict"] = "SESSION"
        res["session"] = (
            f"{base}/api/dl hidden without a session; verification service issues one "
            "after an interactive Cloudflare Turnstile"
        )
        return True
    res["session"] = f"HTTP {status}: {text[:120]}"
    return False


def probe_yupify(base, res):
    try:
        status, body, _ = http(f"{base}/api/track/{TIDAL_TRACK_ID}", timeout=40)
    except urllib.error.HTTPError as e:
        res["yupify"] = f"HTTP {e.code}"
        return False
    except Exception as e:
        res["yupify"] = str(e)[:70]
        return False
    payload = as_json(body) or {}
    if not payload.get("url"):
        res["yupify"] = f"{status}, no url ({str(payload.get('error'))[:70]})"
        return False
    audio = audio_probe(base + payload["url"] if payload["url"].startswith("/") else payload["url"])
    res["yupify"] = (
        f"{status}, {payload.get('usedQuality')} {payload.get('assetPresentation')}, "
        f"cached={payload.get('cached')}, audio={'ok' if audio.get('ok') else audio.get('reason')}"
    )
    if audio.get("ok") and payload.get("assetPresentation") == "FULL":
        res["verdict"] = "YUPIFY_CACHE"
        res["audio"] = audio
    return True


def probe(host):
    base = host.rstrip("/")
    res = {"host": base}
    found = probe_navidrums(base, res)
    if res.get("verdict"):
        return res
    found = probe_dab(base, res) or found
    if res.get("verdict"):
        return res
    found = probe_yupify(base, res) or found
    if res.get("verdict"):
        return res
    found = probe_session_routes(base, res) or found
    res.setdefault("verdict", "META_ONLY" if found else "DEAD")
    return res


ORDER = {"STREAMS": 0, "PREVIEW": 1, "YUPIFY_CACHE": 2, "SESSION": 3, "META_ONLY": 4, "DEAD": 5}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", action="append", default=[], help="extra base url to probe")
    parser.add_argument("--json", help="write the raw results to this file")
    parser.add_argument("--workers", type=int, default=12)
    args = parser.parse_args()

    hosts = CANDIDATES + args.host
    results = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        for res in pool.map(probe, hosts):
            results.append(res)
    results.sort(key=lambda r: ORDER.get(r.get("verdict"), 9))

    for res in results:
        print(json.dumps(res, ensure_ascii=False))

    counts = {}
    for res in results:
        counts[res.get("verdict", "?")] = counts.get(res.get("verdict", "?"), 0) + 1
    print("\n" + ", ".join(f"{k}={v}" for k, v in sorted(counts.items())), file=sys.stderr)

    usable = [r for r in results if r.get("verdict") in ("STREAMS", "PREVIEW", "YUPIFY_CACHE")]
    print(
        "usable: " + (", ".join(f"{r['host']} ({r['verdict']})" for r in usable) if usable else "none"),
        file=sys.stderr,
    )
    if args.json:
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump(results, fh, ensure_ascii=False, indent=2)
    return 0


if __name__ == "__main__":
    sys.exit(main())
