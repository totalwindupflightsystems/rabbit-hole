//go:build ignore
// +build ignore

// collector.bpf.c — eBPF programs for syscall tracing
// Rabbit-Hole: agent legibility platform.
// Requires: Linux 5.8+ (CO-RE), clang for compilation via bpf2go.

#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

#define MAX_ARGS 6
#define MAX_FILENAME 256
#define MAX_STACK_DEPTH 32

// TraceCategory enum (mirrors pkg/types/trace.go)
#define TRACE_CATEGORY_SYSCALL        0
#define TRACE_CATEGORY_NETWORK        1
#define TRACE_CATEGORY_FILE           2
#define TRACE_CATEGORY_LLM_CALL       3
#define TRACE_CATEGORY_CONTEXT_WINDOW 4
#define TRACE_CATEGORY_PROCESS        5
#define TRACE_CATEGORY_RESOURCE       6

struct trace_event {
    __u64 timestamp_ns;
    __u32 pid;
    __u32 tid;
    __u64 syscall_nr;
    __u64 args[MAX_ARGS];
    __s64 ret;
    __u32 errno_val;
    __u64 duration_ns;
    __u32 stack_depth;
    __u64 stack[MAX_STACK_DEPTH];
    __u8 category;
    char comm[16];
    char filename[MAX_FILENAME];
    __u32 saddr;
    __u32 daddr;
    __u16 sport;
    __u16 dport;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u32);
} pid_filter SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
    __uint(key_size, sizeof(__u32));
    __uint(value_size, sizeof(__u32));
} events SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 10240);
    __type(key, __u64);
    __type(value, __u64);
} syscall_start SEC(".maps");

static __always_inline int should_trace(__u32 pid) {
    __u32 key = 0;
    __u32 *target = bpf_map_lookup_elem(&pid_filter, (void *)&key);
    if (!target) return 0;
    return (*target == pid) ? 1 : 0;
}

SEC("tracepoint/raw_syscalls/sys_enter")
int trace_enter_syscall(struct trace_event_raw_sys_enter *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    __u64 tid = bpf_get_current_pid_tgid();
    __u64 entry_key = ((__u64)ctx->id << 32) | (__u32)tid;
    __u64 timestamp = bpf_ktime_get_ns();
    bpf_map_update_elem(&syscall_start, &entry_key, &timestamp, BPF_ANY);
    return 0;
}

SEC("tracepoint/raw_syscalls/sys_exit")
int trace_exit_syscall(struct trace_event_raw_sys_exit *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    __u64 tid = bpf_get_current_pid_tgid();
    __u64 entry_key = ((__u64)ctx->id << 32) | (__u32)tid;
    __u64 *start_ns = bpf_map_lookup_elem(&syscall_start, &entry_key);
    if (!start_ns) return 0;

    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;

    __u64 now = bpf_ktime_get_ns();
    event->timestamp_ns = now;
    event->pid = pid;
    event->tid = (__u32)tid;
    event->syscall_nr = ctx->id;
    event->ret = ctx->ret;
    event->errno_val = (ctx->ret < 0) ? -ctx->ret : 0;
    event->duration_ns = now - *start_ns;
    event->stack_depth = bpf_get_stackid(ctx, &events, BPF_F_USER_STACK);
    event->category = TRACE_CATEGORY_SYSCALL;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;

    bpf_ringbuf_submit(event, 0);
    bpf_map_delete_elem(&syscall_start, &entry_key);
    return 0;
}

// Network probes
SEC("kprobe/tcp_connect")
int trace_connect(void *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    __u64 now = bpf_ktime_get_ns();
    event->timestamp_ns = now;
    event->pid = pid;
    event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 42;
    event->ret = 0;
    event->errno_val = 0;
    event->duration_ns = 0;
    event->stack_depth = 0;
    event->category = TRACE_CATEGORY_NETWORK;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("kprobe/tcp_sendmsg")
int trace_sendmsg(void *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->pid = pid;
    event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 46;
    event->ret = 0; event->errno_val = 0; event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_NETWORK;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("kprobe/tcp_recvmsg")
int trace_recvmsg(void *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->pid = pid;
    event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 47;
    event->ret = 0; event->errno_val = 0; event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_NETWORK;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

// File operation probes
SEC("tracepoint/syscalls/sys_enter_openat")
int trace_openat(struct trace_event_raw_sys_enter *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->pid = pid; event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 257; event->ret = 0; event->errno_val = 0;
    event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_FILE;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    bpf_probe_read_user_str(event->filename, sizeof(event->filename), (void *)ctx->args[1]);
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("tracepoint/syscalls/sys_enter_read")
int trace_read(struct trace_event_raw_sys_enter *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->pid = pid; event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 0; event->args[0] = ctx->args[0]; event->ret = 0;
    event->errno_val = 0; event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_FILE;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("tracepoint/syscalls/sys_enter_write")
int trace_write(struct trace_event_raw_sys_enter *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->pid = pid; event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 1; event->args[0] = ctx->args[0]; event->ret = 0;
    event->errno_val = 0; event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_FILE;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("tracepoint/syscalls/sys_enter_close")
int trace_close(struct trace_event_raw_sys_enter *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->pid = pid; event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 3; event->args[0] = ctx->args[0]; event->ret = 0;
    event->errno_val = 0; event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_FILE;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

// Process lifecycle
SEC("tracepoint/sched/sched_process_exec")
int trace_exec(void *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->pid = pid; event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 59; event->ret = 0; event->errno_val = 0;
    event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_PROCESS;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("tracepoint/sched/sched_process_exit")
int trace_exit(void *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->pid = pid; event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 60; event->ret = 0; event->errno_val = 0;
    event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_PROCESS;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("tracepoint/sched/sched_process_fork")
int trace_fork(void *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->pid = pid; event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 57; event->ret = 0; event->errno_val = 0;
    event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_PROCESS;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

// TLS interception — uprobes on SSL_read/SSL_write
SEC("uprobe/ssl_read")
int uprobe_ssl_read(void *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->pid = pid; event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 0; event->ret = 0; event->errno_val = 0;
    event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_LLM_CALL;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("uprobe/ssl_write")
int uprobe_ssl_write(void *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    event->timestamp_ns = bpf_ktime_get_ns();
    event->pid = pid; event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 0; event->ret = 0; event->errno_val = 0;
    event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_LLM_CALL;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("uretprobe/ssl_read")
int uretprobe_ssl_read(void *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    __u64 now = bpf_ktime_get_ns();
    event->timestamp_ns = now;
    event->pid = pid; event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 0; event->ret = 0; event->errno_val = 0;
    event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_LLM_CALL;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("uretprobe/ssl_write")
int uretprobe_ssl_write(void *ctx) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    if (!should_trace(pid)) return 0;
    struct trace_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return 0;
    __u64 now = bpf_ktime_get_ns();
    event->timestamp_ns = now;
    event->pid = pid; event->tid = (__u32)bpf_get_current_pid_tgid();
    event->syscall_nr = 0; event->ret = 0; event->errno_val = 0;
    event->duration_ns = 0; event->stack_depth = 0;
    event->category = TRACE_CATEGORY_LLM_CALL;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    __builtin_memset(event->filename, 0, sizeof(event->filename));
    event->saddr = 0; event->daddr = 0; event->sport = 0; event->dport = 0;
    bpf_ringbuf_submit(event, 0);
    return 0;
}

char LICENSE[] SEC("license") = "GPL";
