import json
import time
import threading
import requests
from dataclasses import dataclass, field
from typing import Optional, List, Dict, Any
from datetime import datetime, timedelta


@dataclass
class AcquireOptions:
    lease_time: timedelta = field(default_factory=lambda: timedelta(seconds=30))
    wait_timeout: timedelta = field(default_factory=lambda: timedelta(seconds=0))
    queue_mode: str = "fifo"
    priority: int = 0
    try_lock: bool = False
    capacity: int = 1
    mode: str = "write"


@dataclass
class LockHandle:
    namespace: str
    name: str
    token: int
    lease_time: timedelta
    valid: bool = True
    _stop_heartbeat: Optional[threading.Event] = None
    _lock: threading.RLock = field(default_factory=threading.RLock)

    def is_valid(self) -> bool:
        with self._lock:
            return self.valid

    def get_token(self) -> int:
        with self._lock:
            return self.token

    def validate_token(self, token: int) -> bool:
        with self._lock:
            return token >= self.token


class DistLockClient:
    def __init__(self, servers: List[str], client_id: str,
                 fallback_mode: bool = False, max_retries: int = 3,
                 timeout: int = 10):
        self.servers = servers
        self.client_id = client_id
        self.leader_addr: Optional[str] = None
        self.http = requests.Session()
        self.http.timeout = timeout
        self.locks: Dict[str, LockHandle] = {}
        self._lock = threading.RLock()
        self.fallback_mode = fallback_mode
        self.max_retries = max_retries

        self._discover_leader()

    def _discover_leader(self) -> None:
        for server in self.servers:
            try:
                resp = self.http.get(f"{server}/api/v1/leader", timeout=5)
                if resp.status_code == 200:
                    data = resp.json()
                    if data.get("is_leader"):
                        with self._lock:
                            self.leader_addr = server
                        return
                    elif data.get("leader"):
                        with self._lock:
                            self.leader_addr = data["leader"]
                        return
            except Exception:
                continue

        with self._lock:
            if self.servers:
                self.leader_addr = self.servers[0]

    def _get_leader(self) -> Optional[str]:
        with self._lock:
            return self.leader_addr

    def _set_leader(self, addr: str) -> None:
        with self._lock:
            self.leader_addr = addr

    def _request(self, method: str, path: str, body: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
        last_err = None
        for attempt in range(self.max_retries):
            leader = self._get_leader()
            if not leader:
                if self.fallback_mode:
                    return {"success": True, "token": 1}
                raise Exception("No leader available")

            url = f"{leader}{path}"
            try:
                if method == "GET":
                    resp = self.http.get(url, json=body)
                else:
                    resp = self.http.post(url, json=body)

                result = resp.json()

                if result.get("redirect") and result.get("leader"):
                    self._set_leader(result["leader"])
                    continue

                if resp.status_code >= 500:
                    last_err = Exception(f"Server error: {resp.status_code}")
                    self._discover_leader()
                    time.sleep((attempt ** 2) * 0.1)
                    continue

                return result
            except Exception as e:
                last_err = e
                self._discover_leader()
                time.sleep((attempt ** 2) * 0.1)
                continue

        if self.fallback_mode:
            return {"success": True, "token": 1}

        raise Exception(f"All attempts failed: {last_err}")

    def acquire(self, namespace: str, name: str, lock_type: str,
                opts: Optional[AcquireOptions] = None) -> LockHandle:
        if opts is None:
            opts = AcquireOptions()

        body = {
            "namespace": namespace,
            "name": name,
            "type": lock_type,
            "mode": opts.mode,
            "client_id": self.client_id,
            "lease_time": int(opts.lease_time.total_seconds() * 1000000000),
            "wait_timeout": int(opts.wait_timeout.total_seconds() * 1000000000),
            "queue_mode": opts.queue_mode,
            "priority": opts.priority,
            "try_lock": opts.try_lock,
            "capacity": opts.capacity,
        }

        resp = self._request("POST", "/api/v1/locks/acquire", body)

        if not resp.get("success"):
            raise Exception(f"Failed to acquire lock: {resp.get('error')}")

        handle = LockHandle(
            namespace=namespace,
            name=name,
            token=resp.get("token", 0),
            lease_time=opts.lease_time,
            valid=True,
            _stop_heartbeat=threading.Event(),
        )

        thread = threading.Thread(
            target=self._start_heartbeat,
            args=(handle,),
            daemon=True
        )
        thread.start()

        key = f"{namespace}/{name}"
        with self._lock:
            self.locks[key] = handle

        return handle

    def _start_heartbeat(self, handle: LockHandle) -> None:
        interval = handle.lease_time.total_seconds() / 3
        while True:
            time.sleep(interval)

            with handle._lock:
                if not handle.valid or (handle._stop_heartbeat and handle._stop_heartbeat.is_set()):
                    return
                token = handle.token

            body = {
                "namespace": handle.namespace,
                "name": handle.name,
                "client_id": self.client_id,
                "token": token,
            }

            try:
                resp = self._request("POST", "/api/v1/locks/heartbeat", body)
                if not resp.get("success"):
                    with handle._lock:
                        handle.valid = False
                    return
            except Exception:
                self._discover_leader()
                continue

    def release(self, handle: LockHandle) -> None:
        with handle._lock:
            if not handle.valid:
                raise Exception("Lock already released or invalid")
            token = handle.token
            handle.valid = False
            if handle._stop_heartbeat:
                handle._stop_heartbeat.set()

        body = {
            "namespace": handle.namespace,
            "name": handle.name,
            "client_id": self.client_id,
            "token": token,
        }

        resp = self._request("POST", "/api/v1/locks/release", body)

        if not resp.get("success"):
            raise Exception(f"Failed to release lock: {resp.get('error')}")

        key = f"{handle.namespace}/{handle.name}"
        with self._lock:
            if key in self.locks:
                del self.locks[key]

    def close(self) -> None:
        with self._lock:
            for handle in self.locks.values():
                if handle._stop_heartbeat:
                    handle._stop_heartbeat.set()
                with handle._lock:
                    handle.valid = False
            self.locks.clear()
