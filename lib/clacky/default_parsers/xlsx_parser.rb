#!/usr/bin/env ruby
# frozen_string_literal: true
#
# Clacky XLSX Parser — CLI interface
#
# Usage:
#   ruby xlsx_parser.rb <file_path>
#
# Output:
#   stdout — extracted content in Markdown tables (UTF-8)
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

def parse_row(row_node, shared_strings)
  REXML::XPath.match(row_node, ".//c").map do |c|
    v = REXML::XPath.first(c, "v")&.text
    next "" unless v
    c.attributes["t"] == "s" ? (shared_strings[v.to_i] || "") : v
  end
end

def build_markdown_table(rows)
  col_count = rows.map(&:size).max
  lines = []
  rows.each_with_index do |row, i|
    padded = row + [""] * [col_count - row.size, 0].max
    lines << "| #{padded.join(" | ")} |"
    lines << "|#{" --- |" * col_count}" if i == 0
  end
  lines.join("\n")
end

# --- main ---

path = ARGV[0]

if path.nil? || path.empty?
  warn "Usage: ruby xlsx_parser.rb <file_path>"
  exit 1
end

unless File.exist?(path)
  warn "File not found: #{path}"
  exit 1
end

begin
  body = File.binread(path)
  shared_strings = []
  sheet_names    = {}
  sheet_xmls     = {}

  Zip::File.open_buffer(StringIO.new(body)) do |zip|
    ss_entry = zip.find_entry("xl/sharedStrings.xml")
    if ss_entry
      doc = REXML::Document.new(ss_entry.get_input_stream.read)
      REXML::XPath.each(doc, "//si") do |si|
        shared_strings << REXML::XPath.match(si, ".//t").map(&:text).compact.join
      end
    end

    wb_entry = zip.find_entry("xl/workbook.xml")
    if wb_entry
      doc = REXML::Document.new(wb_entry.get_input_stream.read)
      REXML::XPath.each(doc, "//sheet") do |s|
        idx  = s.attributes["sheetId"]
        name = s.attributes["name"]
        sheet_names[idx] = name if idx && name
      end
    end

    zip.each do |entry|
      if entry.name =~ %r{xl/worksheets/sheet(\d+)\.xml}
        sheet_xmls[$1] = entry.get_input_stream.read
      end
    end
  end

  if sheet_xmls.empty?
    warn "Spreadsheet appears to be empty"
    exit 1
  end

  sections = []
  sheet_xmls.keys.sort_by(&:to_i).each do |idx|
    name = sheet_names[idx] || "Sheet#{idx}"
    doc  = REXML::Document.new(sheet_xmls[idx])

    rows = []
    REXML::XPath.each(doc, "//row") do |row|
      cells = parse_row(row, shared_strings)
      rows << cells unless cells.all?(&:empty?)
    end

    next if rows.empty?
    sections << "### #{name}\n\n#{build_markdown_table(rows)}"
  end

  if sections.empty?
    warn "Spreadsheet appears to be empty"
    exit 1
  end

  print sections.join("\n\n")
  exit 0
rescue => e
  warn "Failed to parse XLSX: #{e.message}"
  warn "Tip: ensure rubyzip is installed: gem install rubyzip"
  exit 1
end
