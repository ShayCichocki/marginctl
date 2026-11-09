# Homebrew formula for marginctl.
#
# To publish this via a tap:
#   1. Create a repo named `homebrew-tap` (e.g. github.com/ShayCichocki/homebrew-tap).
#   2. Copy this file to `Formula/marginctl.rb` in that repo.
#   3. Bump `tag` (below) on each release. Building from source needs only Go,
#      so there are no per-arch bottle SHAs to maintain.
#   4. Users then run: `brew install ShayCichocki/tap/marginctl`.
class Marginctl < Formula
  desc "Terminal novel editor with Vim-style editing and optional LLM assistance"
  homepage "https://github.com/ShayCichocki/marginctl"
  license "MIT"
  head "https://github.com/ShayCichocki/marginctl.git", branch: "main"

  # Bump this tag on each release.
  url "https://github.com/ShayCichocki/marginctl.git", tag: "v1.0.0"
  version "1.0.0"

  depends_on "go" => :build

  def install
    ldflags = "-s -w -X main.version=#{version}"
    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/marginctl"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/marginctl --version")
  end
end
