#!/usr/bin/env ruby
# frozen_string_literal: true
#
# Clacky PPTX Parser — CLI interface
#
# Usage:
#   ruby pptx_parser.rb <file_path>
#
# Output:
#   stdout — extracted content in Markdown (UTF-8)
#   stderr — error messages
#   exit 0 — success
#   exit 1 — failure
#
# Dependencies: rubyzip gem (gem install rubyzip)
#
# This file lives in ~/.clacky/parsers/ and can be modified by the LLM.
#
# VERSION: 1

require "zip"
require "rexml/document"
require "stringio"

def extract_text(shape_node)
  paras = []
  REXML::XPath.each(shape_node, ".//a:p") do |para|
    text = REXML::XPath.match(para, ".//a:t").map(&:text).compact.join
    paras << text unless text.strip.empty?
  end
  paras.join("\n")
end

def parse_table(tbl_node)
  rows = []
  REXML::XPath.each(tbl_node, ".//a:tr") do |tr|
    cells = REXML::XPath.match(tr, ".//a:tc").map do |tc|
      REXML::XPath.match(tc, ".//a:t").map(&:text).compact.join(" ").strip
    end
    rows << cells
  end
  return "" if rows.empty?

  col_count = rows.map(&:size).max
  lines = []
  rows.each_with_index do |row, i|
    padded = row + [""] * [col_count - row.size, 0].max
    lines << "| #{padded.join(" | ")} |"
    lines << "|#{" --- |" * col_count}" if i == 0
  end
  lines.join("\n")
end

def parse_slide(doc, slide_num)
  lines = []

  title_text = nil
  REXML::XPath.each(doc, "//p:sp") do |sp|
    ph = REXML::XPath.first(sp, ".//p:ph")
    next unless ph
    ph_type = ph.attributes["type"]
    if ph_type == "title" || ph_type == "ctrTitle"
      title_text = extract_text(sp).strip
      break
    end
  end

  lines << "## Slide #{slide_num}#{title_text && !title_text.empty? ? ": #{title_text}" : ""}"

  REXML::XPath.each(doc, "//p:sp") do |sp|
    ph = REXML::XPath.first(sp, ".//p:ph")
    if ph
      ph_type = ph.attributes["type"]
      next if %w[title ctrTitle sldNum dt ftr].include?(ph_type)
    end

    text = extract_text(sp).strip
    next if text.empty?
    next if text == title_text

    text.each_line do |line|
      lines << "- #{line.rstrip}" unless line.strip.empty?
    end
  end

  REXML::XPath.each(doc, "//a:tbl") do |tbl|
    lines << parse_table(tbl)
  end

  lines.join("\n")
end

# --- main ---

path = ARGV[0]

if path.nil? || path.empty?
  warn "Usage: ruby pptx_parser.rb <file_path>"
  exit 1
end

unless File.exist?(path)
  warn "File not found: #{path}"
  exit 1
end

begin
  body   = File.binread(path)
  slides = {}

  Zip::File.open_buffer(StringIO.new(body)) do |zip|
    zip.each do |entry|
      if entry.name =~ %r{ppt/slides/slide(\d+)\.xml}
        slides[$1.to_i] = entry.get_input_stream.read
      end
    end
  end

  if slides.empty?
    warn "Presentation appears to be empty"
    exit 1
  end

  sections = slides.keys.sort.map do |num|
    doc = REXML::Document.new(slides[num])
    parse_slide(doc, num)
  end.compact

  if sections.empty?
    warn "Presentation appears to be empty"
    exit 1
  end

  print sections.join("\n\n---\n\n")
  exit 0
rescue => e
  warn "Failed to parse PPTX: #{e.message}"
  warn "Tip: ensure rubyzip is installed: gem install rubyzip"
  exit 1
end
