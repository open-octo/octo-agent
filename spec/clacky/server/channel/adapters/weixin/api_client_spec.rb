# frozen_string_literal: true

require "spec_helper"
require "clacky/server/channel/adapters/weixin/api_client"
require "openssl"
require "digest"
require "tempfile"

RSpec.describe Clacky::Channel::Adapters::Weixin::ApiClient do
  let(:token)    { "test-token-abc123" }
  let(:client)   { described_class.new(base_url: described_class::DEFAULT_BASE_URL, token: token) }

  # -------------------------------------------------------------------------
  # AES-128-ECB encryption (private method, tested via send)
  # -------------------------------------------------------------------------
  describe "#aes_ecb_encrypt (private)" do
    it "produces output that can be decrypted back to the original" do
      key  = OpenSSL::Random.random_bytes(16)
      data = "Hello, Weixin file transfer!"

      encrypted = client.send(:aes_ecb_encrypt, data, key)

      cipher = OpenSSL::Cipher.new("AES-128-ECB")
      cipher.decrypt
      cipher.key = key
      decrypted = cipher.update(encrypted) + cipher.final

      expect(decrypted).to eq(data)
    end

    it "adds PKCS7 padding so output length is a multiple of 16" do
      key  = OpenSSL::Random.random_bytes(16)
      data = "x" * 10  # not a multiple of 16

      encrypted = client.send(:aes_ecb_encrypt, data, key)
      expect(encrypted.bytesize % 16).to eq(0)
    end
  end

  # -------------------------------------------------------------------------
  # download_media — aes_key decoding (multiple formats)
  # -------------------------------------------------------------------------
  describe "#download_media" do
    let(:raw_key)   { OpenSSL::Random.random_bytes(16) }
    let(:plaintext) { "fake image data \x00\x01\x02" }
    let(:encrypted) do
      cipher = OpenSSL::Cipher.new("AES-128-ECB")
      cipher.encrypt
      cipher.key = raw_key
      cipher.update(plaintext) + cipher.final
    end

    def stub_cdn_get(c, data)
      allow(c).to receive(:cdn_get).and_return(data)
    end

    it "decrypts when aes_key is base64(raw 16 bytes) — our outbound image encoding" do
      aes_key_b64 = Base64.strict_encode64(raw_key)  # 24-char base64 string
      cdn_media = { "encrypt_query_param" => "ep", "aes_key" => aes_key_b64 }

      stub_cdn_get(client, encrypted)

      result = client.download_media(cdn_media, described_class::MEDIA_TYPE_IMAGE)
      expect(result).to eq(plaintext)
    end

    it "decrypts when aes_key is a plain hex string (32 hex chars) — inbound WeChat client format" do
      hex_key   = raw_key.unpack1("H*")               # 32-char hex string
      cdn_media = { "encrypt_query_param" => "ep", "aes_key" => hex_key }

      stub_cdn_get(client, encrypted)

      result = client.download_media(cdn_media, described_class::MEDIA_TYPE_IMAGE)
      expect(result).to eq(plaintext)
    end

    it "decrypts when aes_key is base64(hex 32 chars) — our outbound non-image encoding" do
      hex_key       = raw_key.unpack1("H*")
      aes_key_b64   = Base64.strict_encode64(hex_key)  # base64 of 32-char hex string
      cdn_media     = { "encrypt_query_param" => "ep", "aes_key" => aes_key_b64 }

      stub_cdn_get(client, encrypted)

      result = client.download_media(cdn_media, described_class::MEDIA_TYPE_FILE)
      expect(result).to eq(plaintext)
    end

    it "raises ApiError when encrypt_query_param is missing" do
      expect {
        client.download_media({ "aes_key" => "abc" }, described_class::MEDIA_TYPE_IMAGE)
      }.to raise_error(described_class::ApiError, /missing encrypt_query_param/)
    end

    it "raises ApiError when aes_key is missing" do
      expect {
        client.download_media({ "encrypt_query_param" => "ep" }, described_class::MEDIA_TYPE_IMAGE)
      }.to raise_error(described_class::ApiError, /missing aes_key/)
    end
  end

  # -------------------------------------------------------------------------
  # media type detection
  # -------------------------------------------------------------------------
  describe "#detect_media_type (private)" do
    {
      "photo.jpg"   => described_class::MEDIA_TYPE_IMAGE,
      "photo.jpeg"  => described_class::MEDIA_TYPE_IMAGE,
      "banner.png"  => described_class::MEDIA_TYPE_IMAGE,
      "clip.gif"    => described_class::MEDIA_TYPE_IMAGE,
      "movie.mp4"   => described_class::MEDIA_TYPE_VIDEO,
      "song.mp3"    => described_class::MEDIA_TYPE_VOICE,
      "note.m4a"    => described_class::MEDIA_TYPE_VOICE,
      "doc.pdf"     => described_class::MEDIA_TYPE_FILE,
      "data.zip"    => described_class::MEDIA_TYPE_FILE,
      "README.md"   => described_class::MEDIA_TYPE_FILE
    }.each do |filename, expected_type|
      it "detects #{filename} as media_type #{expected_type}" do
        expect(client.send(:detect_media_type, filename)).to eq(expected_type)
      end
    end
  end

  # -------------------------------------------------------------------------
  # build_media_item
  # -------------------------------------------------------------------------
  describe "#build_media_item (private)" do
    let(:cdn_media) { { encrypt_query_param: "abc", aes_key: "key123" } }
    let(:raw_bytes) { "fake file content" }

    it "builds an image item (type 2)" do
      item = client.send(:build_media_item, described_class::MEDIA_TYPE_IMAGE, cdn_media, raw_bytes, "photo.jpg")
      expect(item[:type]).to eq(2)
      expect(item[:image_item][:media]).to eq(cdn_media)
    end

    it "builds a file item (type 4) with file_name, md5, and len" do
      item = client.send(:build_media_item, described_class::MEDIA_TYPE_FILE, cdn_media, raw_bytes, "report.pdf")
      expect(item[:type]).to eq(4)
      expect(item.dig(:file_item, :file_name)).to eq("report.pdf")
      expect(item.dig(:file_item, :md5)).to eq(Digest::MD5.hexdigest(raw_bytes))
      expect(item.dig(:file_item, :len)).to eq(raw_bytes.bytesize.to_s)
      expect(item.dig(:file_item, :media)).to eq(cdn_media)
    end

    it "builds a voice item (type 3)" do
      item = client.send(:build_media_item, described_class::MEDIA_TYPE_VOICE, cdn_media, raw_bytes, "voice.mp3")
      expect(item[:type]).to eq(3)
      expect(item[:voice_item][:media]).to eq(cdn_media)
    end

    it "builds a video item (type 5)" do
      item = client.send(:build_media_item, described_class::MEDIA_TYPE_VIDEO, cdn_media, raw_bytes, "clip.mp4")
      expect(item[:type]).to eq(5)
      expect(item[:video_item][:media]).to eq(cdn_media)
    end
  end

  # -------------------------------------------------------------------------
  # send_file — full flow stubbed
  # -------------------------------------------------------------------------
  describe "#send_file" do
    let(:tmp_file) do
      t = Tempfile.new(["testfile", ".pdf"])
      t.binmode
      t.write("fake pdf content")
      t.flush
      t
    end

    after { tmp_file.close! }

    it "calls getuploadurl, cdn_upload, and sendmessage in order" do
      upload_resp    = { "upload_param" => "encrypted-upload-param" }
      download_param = "encrypted-download-param"
      send_resp      = { "ret" => 0 }

      # Stub internal methods to avoid real HTTP calls
      allow(client).to receive(:post).with("getuploadurl", anything).and_return(upload_resp)
      allow(client).to receive(:cdn_upload).and_return(download_param)
      allow(client).to receive(:post).with("sendmessage", anything).and_return(send_resp)

      result = client.send_file(
        to_user_id:    "user123",
        file_path:     tmp_file.path,
        file_name:     "report.pdf",
        context_token: "ctx-token-xyz"
      )

      expect(result).to eq(send_resp)
      expect(client).to have_received(:post).with("getuploadurl", hash_including(
        media_type:  described_class::MEDIA_TYPE_FILE,
        to_user_id:  "user123"
      ))
      expect(client).to have_received(:cdn_upload).with(
        upload_param:    "encrypted-upload-param",
        filekey:         anything,
        encrypted_bytes: anything
      )
      expect(client).to have_received(:post).with("sendmessage", hash_including(
        msg: hash_including(
          to_user_id:    "user123",
          context_token: "ctx-token-xyz"
        )
      ))
    end

    it "raises ApiError when getuploadurl returns no upload_param" do
      allow(client).to receive(:post).with("getuploadurl", anything).and_return({})

      expect {
        client.send_file(
          to_user_id:    "user123",
          file_path:     tmp_file.path,
          context_token: "ctx-token"
        )
      }.to raise_error(described_class::ApiError, /missing upload_param/)
    end
  end
end
