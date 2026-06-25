// Starter build for a GoLand plugin using the IntelliJ Platform Gradle Plugin 2.x.
// Versions are illustrative — bump to whatever's current when you run this.
plugins {
    id("org.jetbrains.kotlin.jvm") version "2.1.20"
    id("org.jetbrains.intellij.platform") version "2.16.0"
}

group = "dev.unfold"
version = "0.1.1"

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        // Compile against the target GoLand (currently 2025.3.1.1 / build 253)
        // so the Go PSI + embedded-editor APIs match the IDE it'll run in.
        goland("2025.3.1.1")
        bundledPlugin("org.jetbrains.plugins.go") // Go PSI
    }
}

intellijPlatform {
    pluginConfiguration {
        ideaVersion {
            // Built against 2025.3 (253); open upper bound so future builds
            // still accept it (otherwise it's auto-stamped to 253.*).
            sinceBuild = "253"
            untilBuild = "299.*"
        }
    }
}

kotlin {
    jvmToolchain(21)
}
