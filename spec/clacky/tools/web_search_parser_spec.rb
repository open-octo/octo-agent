# frozen_string_literal: true

# Parser unit tests using real HTML fixtures captured from each provider.
#
# Fixtures are saved in spec/fixtures/web_search/ and should be refreshed
# periodically (or when a parser breaks) by re-running:
#
#   bin/refresh_search_fixtures   (to be created)
#
# Each test asserts the MINIMUM acceptable quality:
#   - at least 3 results parsed
#   - every result has a non-empty title and a valid http(s) URL
#   - at least one result title/URL contains the keyword "ruby"

RSpec.describe Clacky::Tools::WebSearch do
  let(:tool) { described_class.new }
  let(:fixture_dir) { File.expand_path("../../fixtures/web_search", __dir__) }

  shared_examples "valid search results" do |min_count: 3|
    it "returns at least #{min_count} results" do
      expect(results.length).to be >= min_count
    end

    it "every result has a non-empty title" do
      results.each do |r|
        expect(r[:title]).not_to be_nil
        expect(r[:title].strip).not_to be_empty, "empty title in result: #{r.inspect}"
      end
    end

    it "every result has a valid http(s) URL" do
      results.each do |r|
        expect(r[:url]).to match(/\Ahttps?:\/\/.+/), "invalid URL: #{r[:url].inspect}"
      end
    end

    it "at least one result is relevant to the query 'ruby'" do
      relevant = results.any? do |r|
        r[:title].downcase.include?("ruby") || r[:url].downcase.include?("ruby")
      end
      expect(relevant).to be(true), "No ruby-related result found. Titles: #{results.map { |r| r[:title] }}"
    end
  end

  describe "#parse_duckduckgo_html" do
    let(:html) { File.read(File.join(fixture_dir, "duckduckgo.html")) }
    let(:results) { tool.send(:parse_duckduckgo_html, html, 10) }

    include_examples "valid search results", min_count: 5

    it "URLs are fully resolved (not duckduckgo redirect links)" do
      results.each do |r|
        expect(r[:url]).not_to include("duckduckgo.com/l/?"), "URL should be resolved: #{r[:url]}"
      end
    end

    it "results include snippets" do
      results_with_snippets = results.count { |r| r[:snippet] && !r[:snippet].strip.empty? }
      expect(results_with_snippets).to be >= 3
    end
  end

  describe "#parse_bing_html" do
    let(:html) { File.read(File.join(fixture_dir, "bing.html")) }
    let(:results) { tool.send(:parse_bing_html, html, 10) }

    include_examples "valid search results", min_count: 5

    it "URLs are fully resolved (not bing.com/ck redirect links)" do
      results.each do |r|
        expect(r[:url]).not_to include("bing.com/ck/"), "URL should be resolved: #{r[:url]}"
      end
    end

    it "results include snippets" do
      results_with_snippets = results.count { |r| r[:snippet] && !r[:snippet].strip.empty? }
      expect(results_with_snippets).to be >= 3
    end
  end
end
