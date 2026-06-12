<script lang="ts">
  import StatusTag from '../ui/StatusTag.svelte'
</script>

<div class="perm">
  <div class="perm-header">
    <iconify-icon icon="ant-design:safety-outlined" width="15" style="color:#FAAD14"></iconify-icon>
    <span class="perm-title">Permission needed</span>
    <span style="margin-left:auto">
      <StatusTag status="warning">Awaiting approval</StatusTag>
    </span>
  </div>
  <div class="perm-body">
    <span class="perm-desc">Octo wants to run a shell command outside the workspace. Review before allowing.</span>
    <pre class="perm-cmd"><span style="color:#52C41A">$</span> git push origin fix/ws-reconnect-backoff</pre>
    <div class="perm-actions">
      <button class="deny-btn">
        <iconify-icon icon="ant-design:close-outlined" width="13"></iconify-icon>
        Deny
      </button>
      <span style="margin-left:auto"></span>
      <button class="once-btn">Allow Once</button>
      <button class="session-btn">
        <iconify-icon icon="ant-design:check-outlined" width="13"></iconify-icon>
        Allow for Session
      </button>
    </div>
  </div>
</div>

<style>
.perm { border: 1px solid #FFE58F; border-radius: 12px; background: #FFFBE6; overflow: hidden; }
.perm-header {
  display: flex; align-items: center; gap: 8px;
  padding: 10px 16px; border-bottom: 1px solid #FFE58F;
}
.perm-title { font-size: 14px; font-weight: 600; color: #1F1F1F; }
.perm-body { padding: 14px 16px; display: flex; flex-direction: column; gap: 12px; }
.perm-desc { font-size: 13px; line-height: 1.6; color: rgba(0,0,0,0.65); }
.perm-cmd {
  margin: 0; padding: 10px 12px; background: #1F1F1F; color: #E6E6E6;
  border-radius: 6px; font-size: 12px; line-height: 1.6;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow-x: auto;
}
.perm-actions { display: flex; align-items: center; gap: 8px; padding-top: 2px; }
.deny-btn, .once-btn {
  height: 32px; padding: 0 12px; border: 1px solid #D9D9D9; background: #fff;
  border-radius: 6px; display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: rgba(0,0,0,0.65); cursor: pointer; font-family: inherit;
}
.deny-btn:hover { border-color: #FF4D4F; color: #FF4D4F; }
.once-btn:hover { border-color: #4096FF; color: #4096FF; }
.session-btn {
  height: 32px; padding: 0 14px; border: none; background: #1677FF;
  border-radius: 6px; display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: #fff; cursor: pointer; font-family: inherit;
}
.session-btn:hover { background: #4096FF; }
</style>
