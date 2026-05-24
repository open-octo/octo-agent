# frozen_string_literal: true

require "spec_helper"
require "clacky/server/channel/adapters/weixin/adapter"
require "clacky/server/channel/adapters/weixin/api_client"
require "tempfile"

RSpec.describe Clacky::Channel::Adapters::Weixin::Adapter do
  let(:config) do
    {
      token:         "test-token",
      base_url:      "https://ilinkai.weixin.qq.com",
      allowed_users: []
    }
  end

  let(:adapter) { described_class.new(config) }

  # -------------------------------------------------------------------------
  # extract_files
  # -------------------------------------------------------------------------
  describe "#extract_files (private)" do
    it "returns empty array for text-only item_list" do
      items = [{ "type" => 1, "text_item" => { "text" => "hello" } }]
      expect(adapter.send(:extract_files, items)).to eq([])
    end

    it "extracts image items — downloads and returns data_url" do
      cdn_media = { "encrypt_query_param" => "abc", "aes_key" => "key" }
      items = [{ "type" => 2, "image_item" => { "media" => cdn_media } }]

      # Stub the shared @api_client to return fake JPEG bytes (FF D8 magic)
      fake_jpeg = "\xFF\xD8" + "x" * 10
      allow(adapter.instance_variable_get(:@api_client))
        .to receive(:download_media)
        .with(cdn_media, Clacky::Channel::Adapters::Weixin::ApiClient::MEDIA_TYPE_IMAGE)
        .and_return(fake_jpeg)

      files = adapter.send(:extract_files, items)
      expect(files.size).to eq(1)
      expect(files[0][:type]).to eq(:image)
      expect(files[0][:mime_type]).to eq("image/jpeg")
      expect(files[0][:data_url]).to start_with("data:image/jpeg;base64,")
    end

    it "prefers image_item.aeskey over media.aes_key for image download" do
      cdn_media    = { "encrypt_query_param" => "abc", "aes_key" => "wrong_key" }
      top_aeskey   = "correct_top_level_key"
      items = [{ "type" => 2, "image_item" => { "media" => cdn_media, "aeskey" => top_aeskey } }]

      fake_jpeg = "\xFF\xD8" + "x" * 10
      allow(adapter.instance_variable_get(:@api_client))
        .to receive(:download_media)
        .with({ "encrypt_query_param" => "abc", "aes_key" => top_aeskey },
              Clacky::Channel::Adapters::Weixin::ApiClient::MEDIA_TYPE_IMAGE)
        .and_return(fake_jpeg)

      files = adapter.send(:extract_files, items)
      expect(files.size).to eq(1)
      expect(files[0][:mime_type]).to eq("image/jpeg")
    end

    it "extracts file items with name, md5, len" do
      items = [{
        "type"      => 4,
        "file_item" => {
          "media"     => { "encrypt_query_param" => "p1", "aes_key" => "k1" },
          "file_name" => "report.pdf",
          "md5"       => "abc123",
          "len"       => "1024"
        }
      }]
      files = adapter.send(:extract_files, items)
      expect(files.size).to eq(1)
      expect(files[0][:type]).to eq(:file)
      expect(files[0][:name]).to eq("report.pdf")
      expect(files[0][:md5]).to eq("abc123")
      expect(files[0][:len]).to eq("1024")
    end

    it "extracts voice items" do
      items = [{
        "type"       => 3,
        "voice_item" => { "media" => { "encrypt_query_param" => "v", "aes_key" => "vk" }, "text" => "语音内容" }
      }]
      files = adapter.send(:extract_files, items)
      expect(files.size).to eq(1)
      expect(files[0][:type]).to eq(:voice)
    end

    it "extracts video items" do
      items = [{
        "type"       => 5,
        "video_item" => { "media" => { "encrypt_query_param" => "v2", "aes_key" => "vk2" } }
      }]
      files = adapter.send(:extract_files, items)
      expect(files.size).to eq(1)
      expect(files[0][:type]).to eq(:video)
    end

    it "handles mixed text + file items" do
      items = [
        { "type" => 1, "text_item" => { "text" => "check this file" } },
        { "type" => 4, "file_item" => { "media" => { "encrypt_query_param" => "x", "aes_key" => "y" }, "file_name" => "x.zip" } }
      ]
      files = adapter.send(:extract_files, items)
      expect(files.size).to eq(1)
      expect(files[0][:name]).to eq("x.zip")
    end
  end

  # -------------------------------------------------------------------------
  # send_file
  # -------------------------------------------------------------------------
  describe "#send_file" do
    let(:tmp_file) do
      t = Tempfile.new(["test", ".txt"])
      t.write("hello")
      t.flush
      t
    end

    after { tmp_file.close! }

    before do
      # Inject a context_token for the test user
      adapter.send(:store_context_token, "user42", "ctx-tok")
    end

    it "delegates to ApiClient#send_file and returns message_id nil" do
      # @api_client is created in initialize, so stub the existing instance
      fake_client = adapter.instance_variable_get(:@api_client)
      allow(fake_client).to receive(:send_file).and_return({ "ret" => 0 })

      result = adapter.send_file("user42", tmp_file.path, name: "hello.txt")

      expect(result).to eq({ message_id: nil })
      expect(fake_client).to have_received(:send_file).with(
        to_user_id:    "user42",
        file_path:     tmp_file.path,
        file_name:     "hello.txt",
        context_token: "ctx-tok"
      )
    end

    it "returns message_id nil and logs error when no context_token exists" do
      expect(Clacky::Logger).to receive(:warn).with(/no context_token/)
      result = adapter.send_file("unknown-user", tmp_file.path)
      expect(result).to eq({ message_id: nil })
    end

    it "catches and logs ApiClient errors" do
      fake_client = adapter.instance_variable_get(:@api_client)
      allow(fake_client).to receive(:send_file).and_raise(
        Clacky::Channel::Adapters::Weixin::ApiClient::ApiError.new(500, "CDN error")
      )

      expect(Clacky::Logger).to receive(:error).with(/send_file failed/)
      result = adapter.send_file("user42", tmp_file.path)
      expect(result).to eq({ message_id: nil })
    end
  end

  # -------------------------------------------------------------------------
  # process_message — file-only message allowed
  # -------------------------------------------------------------------------
  describe "#process_message (private)" do
    it "emits event even when text is empty but files are present" do
      events = []
      adapter.instance_variable_set(:@on_message, ->(e) { events << e })
      adapter.instance_variable_set(:@running, true)

      msg = {
        "message_type"  => 1,
        "from_user_id"  => "userX",
        "context_token" => "ctx1",
        "message_id"    => 99,
        "create_time_ms"=> (Time.now.to_f * 1000).to_i,
        "item_list"     => [{
          "type"      => 4,
          "file_item" => {
            "media"     => { "encrypt_query_param" => "ep", "aes_key" => "ak" },
            "file_name" => "data.csv",
            "md5"       => "md5hex",
            "len"       => "512"
          }
        }]
      }

      adapter.send(:process_message, msg)

      expect(events.size).to eq(1)
      expect(events[0][:files].size).to eq(1)
      expect(events[0][:files][0][:name]).to eq("data.csv")
      expect(events[0][:text]).to eq("")
    end

    it "does not emit event when item_list has no usable content" do
      events = []
      adapter.instance_variable_set(:@on_message, ->(e) { events << e })

      msg = {
        "message_type"  => 1,
        "from_user_id"  => "userX",
        "context_token" => "ctx1",
        "message_id"    => 100,
        "item_list"     => []
      }

      adapter.send(:process_message, msg)
      expect(events).to be_empty
    end
  end
end
