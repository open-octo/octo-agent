# frozen_string_literal: true

require "spec_helper"
require "clacky/server/channel/adapters/telegram/api_client"

RSpec.describe Clacky::Channel::Adapters::Telegram::ApiClient do
  let(:token)  { "123456789:test-token" }
  let(:client) { described_class.new(token: token) }

  describe "#unwrap (private)" do
    it "returns result when ok is true" do
      body = { "ok" => true, "result" => { "id" => 42 } }
      expect(client.send(:unwrap, body, "getMe")).to eq({ "id" => 42 })
    end

    it "raises ApiError with code + description when ok is false" do
      body = { "ok" => false, "error_code" => 401, "description" => "Unauthorized" }
      expect { client.send(:unwrap, body, "getMe") }
        .to raise_error(described_class::ApiError) { |e|
          expect(e.code).to eq(401)
          expect(e.description).to eq("getMe: Unauthorized")
        }
    end
  end

  describe "#mime_for (private)" do
    it "maps common extensions" do
      expect(client.send(:mime_for, "a.png")).to  eq("image/png")
      expect(client.send(:mime_for, "a.jpg")).to  eq("image/jpeg")
      expect(client.send(:mime_for, "a.jpeg")).to eq("image/jpeg")
      expect(client.send(:mime_for, "a.gif")).to  eq("image/gif")
      expect(client.send(:mime_for, "a.pdf")).to  eq("application/pdf")
      expect(client.send(:mime_for, "a.txt")).to  eq("text/plain")
    end

    it "falls back to octet-stream for unknown" do
      expect(client.send(:mime_for, "a.xyz")).to eq("application/octet-stream")
    end
  end

  describe "#build_http (private)" do
    it "enables SSL for https URLs and sets the requested read_timeout" do
      uri = URI("https://api.telegram.org/bot/foo")
      http = client.send(:build_http, uri, read_timeout: 99)
      expect(http.use_ssl?).to eq(true)
      expect(http.read_timeout).to eq(99)
      expect(http.open_timeout).to eq(described_class::OPEN_TIMEOUT)
    end
  end

  describe "#initialize" do
    it "strips trailing slash from base_url" do
      c = described_class.new(token: token, base_url: "https://example.com/")
      expect(c.instance_variable_get(:@base_url)).to eq("https://example.com")
    end

    it "falls back to DEFAULT_BASE_URL when base_url is blank" do
      c = described_class.new(token: token, base_url: "")
      expect(c.instance_variable_get(:@base_url)).to eq(described_class::DEFAULT_BASE_URL)
    end
  end
end
