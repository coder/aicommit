class Aicommit < Formula
    desc "AI-powered commit message generator"
    homepage "https://github.com/coder/aicommit"
    url "https://github.com/coder/aicommit.git", revision: "HEAD"
    version "0.0.0" # This will be overridden by the git describe in the Makefile
    license "CC0-1.0"

    depends_on "go" => "1.21"
    depends_on "make" => :build

    def install
        system "make", "build"
        bin.install "bin/aicommit"
    end

    test do
        assert_match(/aicommit \S+/, shell_output("#{bin}/aicommit version"))
    end
end

