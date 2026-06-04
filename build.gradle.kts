// Starter build for a GoLand plugin using the IntelliJ Platform Gradle Plugin 2.x.
// Versions are illustrative — bump to whatever's current when you run this.
plugins {
    id("org.jetbrains.kotlin.jvm") version "1.9.24"
    id("org.jetbrains.intellij.platform") version "2.0.1"
}

group = "dev.unfold"
version = "0.1.0"

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        // Compile/run against GoLand so the Go PSI API is available.
        // Alternatively: idea ("IU", "2024.1.4") + bundledPlugin("org.jetbrains.plugins.go").
        goland("2024.1.4")
        bundledPlugin("org.jetbrains.plugins.go") // Go PSI
        instrumentationTools()
    }
}

intellijPlatform {
    pluginConfiguration {
        ideaVersion {
            // Compiled against 2024.1 (241) using only stable APIs, but allow
            // installing on everything newer — otherwise the upper bound is
            // auto-stamped to 241.* and recent GoLand refuses the plugin.
            sinceBuild = "241"
            untilBuild = "299.*"
        }
    }
}

kotlin {
    jvmToolchain(17)
}
