<script lang="ts">
  import QRCode from 'qrcode'

  let { text, size = 220 }: { text: string; size?: number } = $props()

  let dataUrl = $state('')
  let failed = $state(false)

  // Re-encode whenever the text or size changes. The QR carries its own white
  // quiet-zone (margin + padding), so it scans on both light and dark themes.
  $effect(() => {
    if (!text) {
      dataUrl = ''
      return
    }
    QRCode.toDataURL(text, { margin: 1, width: size, errorCorrectionLevel: 'M' })
      .then((url) => {
        dataUrl = url
        failed = false
      })
      .catch(() => {
        dataUrl = ''
        failed = true
      })
  })
</script>

{#if dataUrl}
  <img class="qr" src={dataUrl} alt="pairing QR code" width={size} height={size} />
{:else if failed}
  <div class="qr-error">QR render failed</div>
{/if}

<style>
  .qr {
    display: block;
    background: #fff;
    padding: 8px;
    border-radius: 8px;
  }
  .qr-error {
    font-size: 12px;
    color: var(--error, #dc2626);
  }
</style>
