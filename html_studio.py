#!/usr/bin/env python3
"""独立的 Chat Completions HTML 生成器。Python 3.10+，无需第三方依赖。

运行：python html_studio.py
作品默认保存在脚本旁的 output/html-studio/artworks/；连接信息仅用于本次运行。
"""

import argparse
import http.client
import json
import math
import os
from pathlib import Path
import platform
import re
import shutil
import socket
import sys
import threading
import time
import unicodedata
from urllib.parse import urlsplit, urlunsplit
import uuid
import webbrowser


DEFAULT_PROMPT = (
    "请生成可直接运行的单文件HTML，使用内联SVG绘制一只鹈鹕骑自行车的二维循环动画。"
    "画面需要以鹈鹕和自行车为主体，展示清晰的身体结构、脚踏运动和车轮转动，"
    "并有协调的背景与配色。动画应流畅自然、衔接连续，布局适配不同屏幕尺寸。"
    "仅允许使用 CSS @keyframes 或 SMIL（animate/animateTransform）实现，"
    "不要使用 JavaScript 或 <script> 标签，禁止引用外部资源。只输出完整HTML，"
    "不要代码围栏或解释文字。"
)
SYSTEM_PROMPT = "You are a helpful assistant."
ROOT = Path(__file__).resolve().parent / "output" / "html-studio"
MAX_OUTPUT_BYTES = 16 * 1024 * 1024


class StudioError(Exception):
    pass


def display_width(text):
    return sum(0 if unicodedata.combining(c) else 2 if unicodedata.east_asian_width(c) in "WF" else 1 for c in text)


def clip(text, width):
    text = str(text)
    if display_width(text) <= width:
        return text
    result = ""
    for char in text:
        if display_width(result + char) > max(0, width - 1):
            break
        result += char
    return result + "…"


def plain(text):
    # Provider-controlled model IDs/errors must not execute terminal escape codes.
    return "".join(c if c.isprintable() else " " for c in str(text))


def enable_ansi():
    if not sys.stdout.isatty() or os.getenv("TERM") == "dumb":
        return False
    if os.name != "nt":
        return True
    try:
        import ctypes
        from ctypes import wintypes
        kernel = ctypes.windll.kernel32
        kernel.GetStdHandle.argtypes = [wintypes.DWORD]
        kernel.GetStdHandle.restype = wintypes.HANDLE
        handle = kernel.GetStdHandle(-11 & 0xFFFFFFFF)
        mode = wintypes.DWORD()
        return bool(kernel.GetConsoleMode(handle, ctypes.byref(mode)) and kernel.SetConsoleMode(handle, mode.value | 4))
    except (AttributeError, OSError):
        return False


class UI:
    def __init__(self):
        self.ansi = enable_ansi()
        self.width = max(36, min(88, shutil.get_terminal_size((88, 24)).columns - 2))

    def color(self, text, code="36"):
        return f"\033[{code}m{text}\033[0m" if self.ansi else text

    def banner(self):
        print()
        print(self.color("  ◇  HTML STUDIO", "1;36"))
        print(self.color("  从一个想法，到浏览器里的作品", "90"))
        print(self.color("  " + "─" * (self.width - 2), "90"))

    def step(self, number, title):
        print("\n" + self.color(f"  {number:02d}", "36") + "  " + self.color(title, "1"))

    def note(self, text):
        print("  " + self.color(plain(text), "90"))

    def success(self, text):
        print("  " + self.color("✓ " + plain(text), "32"))

    def error(self, text):
        print("\n  " + self.color("× " + plain(text), "31"), file=sys.stderr)


def normalize_base(value):
    parsed = urlsplit(value.strip())
    if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.password:
        raise StudioError("Base URL 必须是 http(s) 地址，Key 请单独输入。")
    if parsed.query or parsed.fragment:
        raise StudioError("Base URL 请勿包含查询参数或 #fragment。")
    try:
        parsed.port
    except ValueError as exc:
        raise StudioError("Base URL 的端口无效。") from exc
    path = parsed.path.rstrip("/")
    for suffix in ("/chat/completions", "/models"):
        if path.endswith(suffix):
            path = path[:-len(suffix)]
            break
    if not path:
        path = "/v1"
    return urlunsplit(parsed._replace(path=path))


def user_agent():
    system = platform.system().lower()
    name = "win32" if system == "windows" else system
    release = platform.version() if system == "windows" else platform.release()
    machine = platform.machine().lower()
    arch = {"amd64": "x64", "x86_64": "x64", "aarch64": "arm64"}.get(machine, machine)
    return f"pi ({name} {release}; {arch})"


def connection_for(url, timeout):
    target = urlsplit(url)
    cls = http.client.HTTPSConnection if target.scheme == "https" else http.client.HTTPConnection
    return cls(target.hostname, target.port, timeout=timeout), target.path or "/"


def request_headers(key):
    return {"Authorization": "Bearer " + key, "Content-Type": "application/json", "User-Agent": user_agent()}


def error_message(value):
    if isinstance(value, dict):
        error = value.get("error", value)
        if isinstance(error, dict):
            return str(error.get("message") or error.get("type") or error)
        return str(error)
    return str(value)


def http_failure(response):
    raw = response.read(65536).decode("utf-8", errors="replace")
    try:
        message = error_message(json.loads(raw))
    except ValueError:
        message = raw or response.reason
    return StudioError(f"HTTP {response.status}：{message[:1200]}")


def fetch_models(base, key, timeout=30):
    connection, path = connection_for(base + "/models", timeout)
    try:
        connection.request("GET", path, headers=request_headers(key))
        response = connection.getresponse()
        if response.status != 200:
            raise http_failure(response)
        raw = response.read(4 * 1024 * 1024 + 1)
        if len(raw) > 4 * 1024 * 1024:
            raise StudioError("模型列表过大。")
        body = json.loads(raw)
        data = body.get("data", []) if isinstance(body, dict) else body
        if not isinstance(data, list):
            raise StudioError("模型列表不是有效的 data 数组。")
        models = sorted({item["id"] for item in data if isinstance(item, dict) and isinstance(item.get("id"), str) and item["id"].strip()}, key=str.casefold)
        if not models:
            raise StudioError("接口没有返回可选模型，请检查 /models 地址和 Key 权限。")
        return models
    finally:
        connection.close()


class KeyReader:
    def __enter__(self):
        if os.name != "nt":
            import termios
            import tty
            self.fd = sys.stdin.fileno()
            self.saved = termios.tcgetattr(self.fd)
            tty.setcbreak(self.fd)
        return self

    def __exit__(self, *exc):
        if os.name != "nt":
            import termios
            termios.tcsetattr(self.fd, termios.TCSADRAIN, self.saved)
    def __call__(self):
        if os.name == "nt":
            import msvcrt
            char = msvcrt.getwch()
            if char in ("\x00", "\xe0"):
                return {"H": "up", "P": "down", "G": "home", "O": "end", "I": "pageup", "Q": "pagedown"}.get(msvcrt.getwch(), "")
        else:
            import select
            first = os.read(self.fd, 1)
            if not first:
                return "escape"
            count = 1 if first[0] < 128 else 2 if first[0] < 224 else 3 if first[0] < 240 else 4
            encoded = first
            while len(encoded) < count:
                extra = os.read(self.fd, count - len(encoded))
                if not extra:
                    return "escape"
                encoded += extra
            char = encoded.decode(sys.stdin.encoding or "utf-8", errors="replace")
            if char == "\x1b":
                sequence = ""
                while select.select([self.fd], [], [], 0.04)[0]:
                    sequence += os.read(self.fd, 1).decode("utf-8", errors="ignore")
                    if sequence[-1:].isalpha() or sequence.endswith("~"):
                        break
                return {"[A": "up", "[B": "down", "[H": "home", "[F": "end", "[5~": "pageup", "[6~": "pagedown"}.get(sequence, "escape")
        if char in ("\r", "\n"):
            return "enter"
        if char in ("\x08", "\x7f"):
            return "backspace"
        if char in ("\x03", "\x1b", "\x04"):
            return "escape"
        return char if char.isprintable() else ""


def choose_model(models, previous, ui, read_key=None):
    if read_key is None and (not sys.stdin.isatty() or not ui.ansi):
        for index, model in enumerate(models, 1):
            print(f"  {index:>3}  {plain(model)}")
        default = models.index(previous) + 1 if previous in models else 1
        while True:
            value = input(f"\n  选择模型编号 [{default}]: ").strip()
            if not value:
                return models[default - 1]
            if value.isdigit() and 1 <= int(value) <= len(models):
                return models[int(value) - 1]
            ui.note("请输入列表内的编号。")
    if read_key is None:
        with KeyReader() as reader:
            return choose_model(models, previous, ui, reader)
    selected = models.index(previous) if previous in models else 0
    query, drawn = "", 0
    rows = max(3, min(8, shutil.get_terminal_size((88, 24)).lines - 12))
    sys.stdout.write("\033[?25l")
    try:
        while True:
            filtered = [model for model in models if query.casefold() in model.casefold()]
            selected = min(selected, max(0, len(filtered) - 1))
            start = min(max(0, selected - rows // 2), max(0, len(filtered) - rows))
            lines = [ui.color("  ↑ ↓ 选择 · 输入筛选 · 回车确认 · Esc 退出", "90"),
                     "  筛选：" + (plain(query) or ui.color("全部模型", "90"))]
            for position in range(start, start + rows):
                if position < len(filtered):
                    label = clip(plain(filtered[position]), ui.width - 7)
                    lines.append(ui.color("  › " + label, "1;36") if position == selected else "    " + label)
                else:
                    lines.append("    " + ("没有匹配模型" if not filtered and position == start else ""))
            lines.append(ui.color(f"  {selected + 1 if filtered else 0}/{len(filtered)} · 共 {len(models)} 个模型", "90"))
            if drawn:
                sys.stdout.write(f"\033[{drawn}A")
            for line in lines:
                sys.stdout.write("\r\033[2K" + line + "\n")
            sys.stdout.flush()
            drawn = len(lines)
            key = read_key()
            if key == "escape":
                raise KeyboardInterrupt()
            if key == "enter" and filtered:
                return filtered[selected]
            if key in ("up", "down", "pageup", "pagedown") and filtered:
                jump = {"up": -1, "down": 1, "pageup": -rows, "pagedown": rows}[key]
                selected = (selected + jump) % len(filtered)
            elif key == "home":
                selected = 0
            elif key == "end":
                selected = max(0, len(filtered) - 1)
            elif key == "backspace":
                query, selected = query[:-1], 0
            elif len(key) == 1 and key.isprintable():
                query, selected = query + key, 0
    finally:
        sys.stdout.write("\033[?25h")
        sys.stdout.flush()


class Generation:
    def __init__(self):
        self.started = time.monotonic()
        self.parts = []
        self.reasoning_chars = 0
        self.finished = None
        self.estimated_tokens = 0.0
        self.text_bytes = 0
        self.events = 0
        self.usage = {}
        self.finish_reason = None
        self.ended_by = None
        self.complete = False
        self.phase = "等待模型响应"
        self.lock = threading.Lock()

    def add(self, content, reasoning):
        with self.lock:
            if content:
                self.parts.append(content)
                self.text_bytes += len(content.encode("utf-8"))
                self.phase = "正在生成 HTML"
            elif reasoning:
                self.phase = "模型正在思考"
            self.reasoning_chars += len(reasoning)
            for text in (content, reasoning):
                self.estimated_tokens += sum(0.25 if ord(c) < 128 else 0.67 for c in text)
            if self.text_bytes > MAX_OUTPUT_BYTES:
                raise StudioError("生成内容超过 16 MiB，已停止并保留已接收内容。")

    def metrics(self):
        with self.lock:
            elapsed = max((self.finished or time.monotonic()) - self.started, 0.001)
            exact = isinstance(self.usage.get("completion_tokens"), int)
            tokens = self.usage["completion_tokens"] if exact else math.ceil(self.estimated_tokens)
            return {"elapsed": elapsed, "tokens": tokens, "exact": exact,
                    "tps": tokens / elapsed, "bytes": self.text_bytes, "phase": self.phase}

    @property
    def text(self):
        return "".join(self.parts)


class LiveProgress:
    def __init__(self, state, ui):
        self.state, self.ui = state, ui
        self.stop = threading.Event()
        self.thread = None

    def render(self, final=False):
        metrics = self.state.metrics()
        mark = "" if metrics["exact"] else "≈"
        seconds = int(metrics["elapsed"])
        spinner = "✓" if final and self.state.complete else "·" if final else "◐◓◑◒"[int(metrics["elapsed"] * 6) % 4]
        line = (f"  {spinner} {metrics['phase']}  {seconds // 60:02d}:{seconds % 60:02d}"
                f"  | 已收 {mark}{metrics['tokens']:,} token"
                f"  | 均速 {mark}{metrics['tps']:.1f} tok/s"
                f"  | HTML {metrics['bytes'] / 1024:.1f} KB")
        if self.ui.ansi:
            sys.stdout.write("\r\033[2K" + self.ui.color(clip(line, self.ui.width), "36"))
            sys.stdout.flush()
        else:
            print(line, flush=True)

    def __enter__(self):
        def update():
            self.render()
            while not self.stop.wait(0.15 if self.ui.ansi else 5):
                self.render()
        if self.ui.ansi:
            sys.stdout.write("\033[?25l")
        self.thread = threading.Thread(target=update, daemon=True)
        self.thread.start()
        return self

    def __exit__(self, *exc):
        self.stop.set()
        self.thread.join()
        self.render(final=True)
        if self.ui.ansi:
            sys.stdout.write("\033[?25h\n")
            sys.stdout.flush()


def sse_events(response):
    data = []
    while True:
        raw = response.readline(1024 * 1024 + 1)
        if len(raw) > 1024 * 1024:
            raise StudioError("上游单条流式事件过大。")
        if not raw:
            if data:
                yield "\n".join(data)
            return
        line = raw.decode("utf-8", errors="replace").rstrip("\r\n")
        if not line:
            if data:
                yield "\n".join(data)
                data = []
        elif line.startswith("data:"):
            value = line[5:]
            data.append(value[1:] if value.startswith(" ") else value)


def build_payload(model, prompt, max_tokens):
    return {"model": model, "messages": [{"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": prompt}], "stream": True,
            "max_completion_tokens": max_tokens, "store": False,
            "stream_options": {"include_usage": True}}


def generate(base, key, payload, state, timeout=900):
    connection, path = connection_for(base + "/chat/completions", timeout)
    active_socket = None
    expired = threading.Event()

    def expire():
        expired.set()
        transport = active_socket or connection.sock
        if transport is not None:
            try:
                transport.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass

    timer = threading.Timer(timeout, expire)
    timer.daemon = True
    timer.start()
    try:
        headers = request_headers(key)
        headers["Accept"] = "text/event-stream"
        connection.request("POST", path, json.dumps(payload, ensure_ascii=False).encode("utf-8"), headers)
        response = connection.getresponse()
        active_socket = getattr(getattr(response.fp, "raw", None), "_sock", None) or connection.sock
        if response.status != 200:
            raise http_failure(response)
        for event in sse_events(response):
            if expired.is_set():
                raise StudioError("请求已达到超时时间。")
            if event.strip() == "[DONE]":
                state.complete, state.ended_by = True, "[DONE]"
                break
            data = json.loads(event)
            if not isinstance(data, dict):
                raise StudioError("上游返回了无效的流式数据。")
            state.events += 1
            if data.get("error") or data.get("type") == "error":
                raise StudioError(error_message(data))
            if isinstance(data.get("usage"), dict):
                with state.lock:
                    state.usage = data["usage"]
            choices = data.get("choices") or []
            if not isinstance(choices, list):
                raise StudioError("上游 choices 字段格式不正确。")
            for choice in choices:
                if not isinstance(choice, dict) or choice.get("index", 0) != 0:
                    continue
                delta = choice.get("delta") or {}
                if not isinstance(delta, dict):
                    continue
                content = delta.get("content") if isinstance(delta.get("content"), str) else ""
                reasoning = next((delta[field] for field in ("reasoning_content", "reasoning", "reasoning_text") if isinstance(delta.get(field), str) and delta[field]), "")
                state.add(content, reasoning)
                if choice.get("finish_reason"):
                    state.finish_reason = choice["finish_reason"]
        if expired.is_set():
            raise StudioError("请求已达到超时时间。")
        if not state.complete and state.finish_reason:
            state.complete, state.ended_by = True, "finish_reason + EOF"
        if not state.complete:
            raise StudioError("响应流提前断开，未收到完成标记。")
        if not state.text.strip():
            raise StudioError("模型正常结束，但未返回 HTML 正文。")
        with state.lock:
            state.phase = "接收完成"
    finally:
        with state.lock:
            state.finished = time.monotonic()
            if not state.complete:
                state.phase = "生成已停止"
        timer.cancel()
        connection.close()


def extract_html(text):
    match = re.search(r"<html(?:\s|>)", text, re.IGNORECASE)
    ends = list(re.finditer(r"</html\s*>", text, re.IGNORECASE))
    if not match or not ends or ends[-1].start() < match.start():
        return ""
    start = match.start()
    doctype = re.search(r"<!doctype\s+html\s*>", text[:start], re.IGNORECASE)
    if doctype:
        start = doctype.start()
    return text[start:ends[-1].end()].strip()


def file_stem(base, model):
    parsed = urlsplit(base)

    def safe(value, limit):
        value = re.sub(r"[^\w.-]+", "_", value, flags=re.UNICODE).strip(" ._")
        return (value or "unknown")[:limit]

    site = safe(parsed.netloc + parsed.path, 64)
    return f"{site}__{safe(model, 64)}__{time.strftime('%Y%m%d-%H%M%S')}-{uuid.uuid4().hex[:6]}"


def save_result(output, stem, html, state, base, model, prompt, key):
    output.mkdir(parents=True, exist_ok=True)
    path = output / (stem + ".html")
    with path.open("x", encoding="utf-8", newline="\n") as stream:
        stream.write(html)
    metrics = state.metrics()
    metadata = {"base_url": base, "model": model, "prompt": prompt, "usage": state.usage,
                "estimated_tokens": None if metrics["exact"] else metrics["tokens"],
                "duration_seconds": round(metrics["elapsed"], 3), "average_tokens_per_second": round(metrics["tps"], 2),
                "finish_reason": state.finish_reason, "ended_by": state.ended_by, "html_file": path.name}
    (output / (stem + ".json")).write_text(json.dumps(metadata, ensure_ascii=False, indent=2).replace(key, "<REDACTED>"), encoding="utf-8")
    return path


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--base-url", help="站点地址或 API 前缀；裸域名自动补 /v1")
    parser.add_argument("--model", help="跳过选择，使用 /models 列表中的指定模型")
    parser.add_argument("--prompt", help="生成需求；默认交互输入，回车使用鹈鹕骑车动画")
    parser.add_argument("--prompt-file", type=Path, help="从 UTF-8 文件读取多行生成需求")
    parser.add_argument("--output", type=Path, default=ROOT / "artworks", help="HTML 作品保存目录")
    parser.add_argument("--max-tokens", type=int, default=16384)
    parser.add_argument("--timeout", type=float, default=900, help="每次生成的总超时秒数")
    parser.add_argument("--no-open", action="store_true", help="保存后不自动打开浏览器")
    args = parser.parse_args(argv)
    if args.max_tokens < 16 or args.timeout <= 0 or not math.isfinite(args.timeout):
        parser.error("max-tokens 必须 >=16，timeout 必须是有限正数")
    if args.prompt is not None and args.prompt_file:
        parser.error("--prompt 和 --prompt-file 不能同时使用")
    ui = UI()
    key, state, stem, base, model = "", None, None, "", ""
    try:
        ui.banner()
        ui.step(1, "连接你的模型服务")
        base_value = args.base_url or input("  Base URL: ").strip()
        base = normalize_base(base_value)
        key = input("  API Key: ").strip()
        if not key:
            raise StudioError("API Key 不能为空。")
        ui.note(base)
        ui.note("正在获取模型列表…")
        models = fetch_models(base, key, min(args.timeout, 30))
        ui.step(2, "选择生成模型")
        model = args.model
        if model and model not in models:
            raise StudioError(f"模型列表中没有 {model}。")
        if not model:
            model = choose_model(models, "", ui)
        ui.success("已选择 " + model)
        ui.step(3, "描述你的 HTML 作品")
        if args.prompt_file:
            prompt = args.prompt_file.read_text(encoding="utf-8-sig").strip()
        elif args.prompt is not None:
            prompt = args.prompt.strip()
        else:
            ui.note("直接回车：鹈鹕骑车 SVG 动画；也可输入自己的需求。")
            prompt = input("  生成需求: ").strip()
        prompt = prompt or DEFAULT_PROMPT
        if prompt != DEFAULT_PROMPT:
            prompt = "请生成可直接在浏览器打开的完整单文件 HTML。只输出完整 HTML，不要代码围栏或解释。\n\n" + prompt
        ui.note(clip(prompt.replace("\n", " "), ui.width - 2))
        ui.note("流式 token 为估算；结束后使用上游准确用量。均速包含等待时间。")
        print()
        state = Generation()
        stem = file_stem(base, model)
        with LiveProgress(state, ui):
            generate(base, key, build_payload(model, prompt, args.max_tokens), state, args.timeout)
        html = extract_html(state.text)
        if not html:
            suffix = "输出达到 token 上限。" if state.finish_reason == "length" else ""
            raise StudioError(suffix + "没有收到完整 <html>…</html> 文档，请调整需求或 --max-tokens。")
        path = save_result(args.output, stem, html, state, base, model, prompt, key)
        print()
        ui.success("HTML 已保存")
        ui.note(str(path.resolve()))
        usage = state.usage
        if usage:
            reasoning = usage.get("completion_tokens_details") or {}
            reasoning = reasoning.get("reasoning_tokens") if isinstance(reasoning, dict) else None
            ui.note(f"输入 {usage.get('prompt_tokens', '—')} · 输出 {usage.get('completion_tokens', '—')} · 总计 {usage.get('total_tokens', '—')} token" + (f" · 推理 {reasoning}" if reasoning is not None else ""))
        if not args.no_open:
            try:
                opened = webbrowser.open(path.resolve().as_uri(), new=2)
            except (OSError, webbrowser.Error):
                opened = False
            if opened:
                ui.success("已交给默认浏览器打开")
            else:
                ui.note("浏览器未能自动打开，请双击上面的 HTML 文件。")
        print()
        return 0
    except (KeyboardInterrupt, EOFError):
        ui.note("已取消。")
        return 130
    except (StudioError, OSError, ValueError, http.client.HTTPException) as exc:
        message = str(exc).replace(key, "<REDACTED>") if key else str(exc)
        ui.error(message)
        return 1
    finally:
        if state and state.text and stem and (not state.complete or not extract_html(state.text)):
            try:
                args.output.mkdir(parents=True, exist_ok=True)
                partial = args.output / (stem + ".partial.txt")
                partial.write_text(state.text.replace(key, "<REDACTED>"), encoding="utf-8")
                ui.note(f"已接收的内容保存在：{partial.resolve()}")
            except OSError:
                ui.note("部分内容保存失败，请检查输出目录权限。")


if __name__ == "__main__":
    for stream in (sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8", errors="replace")
    raise SystemExit(main())
