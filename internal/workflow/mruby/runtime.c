/* runtime.c — the mruby workflow runtime, compiled to wasm32-wasi and run by
   wazero (internal/workflow). It is deliberately thin: read the full Ruby
   source from stdin (the Go side concatenates prelude.rb + the user script),
   evaluate it, print the final value to stdout. All side effects go through
   host imports — there is no file/network access in the wasm sandbox.

   Build: see scripts/build-mruby-wasm.sh. The artifact is committed at
   internal/workflow/mruby.wasm and go:embed'd; it only needs rebuilding when a
   host primitive is added/removed or mruby is upgraded — the DSL prelude lives
   in Go, so changing the DSL does NOT require rebuilding this. */
#include <mruby.h>
#include <mruby/compile.h>
#include <mruby/string.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>

/* ── Host imports (Go, module "env"). Strings cross as (ptr,len) in linear
   memory; results are written back into a guest-allocated buffer. ───────── */
__attribute__((import_module("env"), import_name("agent_start")))
extern int host_agent_start(const char *p, int plen,
                            const char *model, int mlen,
                            const char *tools, int tlen,
                            int read_only,
                            const char *schema, int slen,
                            const char *isolation, int ilen);  /* -> token, non-blocking */
__attribute__((import_module("env"), import_name("agent_wait_any")))
extern int host_agent_wait_any(void);                          /* blocks; -> token (0 = cancelled) */
__attribute__((import_module("env"), import_name("agent_take")))
extern int host_agent_take(int token, char *out, int outcap);  /* -> result length */
__attribute__((import_module("env"), import_name("log")))
extern void host_log(const char *p, int plen);
__attribute__((import_module("env"), import_name("budget_remaining")))
extern long long host_budget_remaining(void);                  /* -> remaining output-token budget */
__attribute__((import_module("env"), import_name("args")))
extern int host_args(char *out, int outcap);                   /* -> args-JSON length (0 = none) */

static mrb_value m_agent_start(mrb_state *mrb, mrb_value self) {
  const char *p; mrb_int plen;
  const char *model; mrb_int mlen;
  const char *tools; mrb_int tlen;
  mrb_int read_only;
  const char *schema; mrb_int slen;
  const char *isolation; mrb_int ilen;
  /* prompt, model, tools (comma-joined names), read_only(0/1), schema(JSON),
     isolation — the prelude always passes all six (empty / 0 when unset). */
  mrb_get_args(mrb, "sssiss", &p, &plen, &model, &mlen, &tools, &tlen, &read_only, &schema, &slen, &isolation, &ilen);
  return mrb_fixnum_value(host_agent_start(p, (int)plen, model, (int)mlen,
                                           tools, (int)tlen, (int)read_only,
                                           schema, (int)slen,
                                           isolation, (int)ilen));
}
static mrb_value m_agent_wait_any(mrb_state *mrb, mrb_value self) {
  (void)self;
  return mrb_fixnum_value(host_agent_wait_any());
}
static mrb_value m_agent_take(mrb_state *mrb, mrb_value self) {
  mrb_int token;
  mrb_get_args(mrb, "i", &token);
  int cap = 16 << 20; /* 16 MiB result cap (a sub-agent reply rarely nears this) */
  char *buf = (char *)malloc(cap);
  if (!buf) return mrb_str_new(mrb, "", 0);
  int n = host_agent_take((int)token, buf, cap);
  mrb_value r = mrb_str_new(mrb, buf, n < 0 ? 0 : n);
  free(buf);
  return r;
}
static mrb_value m_log(mrb_state *mrb, mrb_value self) {
  const char *p; mrb_int len;
  mrb_get_args(mrb, "s", &p, &len);
  host_log(p, (int)len);
  return mrb_nil_value();
}
static mrb_value m_budget_remaining(mrb_state *mrb, mrb_value self) {
  (void)self;
  return mrb_int_value(mrb, (mrb_int)host_budget_remaining());
}
static mrb_value m_args(mrb_state *mrb, mrb_value self) {
  (void)self;
  int cap = 16 << 20; /* 16 MiB: the args JSON is small, but keep parity with agent_take */
  char *buf = (char *)malloc(cap);
  if (!buf) return mrb_str_new(mrb, "", 0);
  int n = host_args(buf, cap);
  mrb_value r = mrb_str_new(mrb, buf, n < 0 ? 0 : n);
  free(buf);
  return r;
}

/* Read all of stdin into a heap buffer (the Go side pipes prelude+script). */
static char *read_all_stdin(size_t *out_len) {
  size_t cap = 1 << 16, len = 0;
  char *buf = (char *)malloc(cap);
  if (!buf) return NULL;
  size_t n;
  while ((n = fread(buf + len, 1, cap - len, stdin)) > 0) {
    len += n;
    if (len == cap) {
      cap *= 2;
      char *nb = (char *)realloc(buf, cap);
      if (!nb) { free(buf); return NULL; }
      buf = nb;
    }
  }
  buf[len] = '\0';
  *out_len = len;
  return buf;
}

int main(void) {
  mrb_state *mrb = mrb_open();
  if (!mrb) { fprintf(stderr, "mrb_open failed\n"); return 1; }
  struct RClass *k = mrb->kernel_module;
  mrb_define_method(mrb, k, "__agent_start",      m_agent_start,      MRB_ARGS_REQ(6));
  mrb_define_method(mrb, k, "__agent_wait_any",   m_agent_wait_any,   MRB_ARGS_NONE());
  mrb_define_method(mrb, k, "__agent_take",       m_agent_take,       MRB_ARGS_REQ(1));
  mrb_define_method(mrb, k, "__log",              m_log,              MRB_ARGS_REQ(1));
  mrb_define_method(mrb, k, "__budget_remaining", m_budget_remaining, MRB_ARGS_NONE());
  mrb_define_method(mrb, k, "__args",             m_args,             MRB_ARGS_NONE());

  size_t len = 0;
  char *src = read_all_stdin(&len);
  if (!src) { fprintf(stderr, "read stdin failed\n"); mrb_close(mrb); return 1; }

  mrb_value r = mrb_load_string(mrb, src);
  free(src);
  if (mrb->exc) {
    mrb_print_error(mrb); /* writes the backtrace to stderr */
    mrb_close(mrb);
    return 2;
  }
  /* Print the workflow's final value to stdout for the host to capture. */
  mrb_value s = mrb_obj_as_string(mrb, r);
  fwrite(RSTRING_PTR(s), 1, RSTRING_LEN(s), stdout);
  fputc('\n', stdout);
  mrb_close(mrb);
  return 0;
}
