#!/usr/bin/env ruby

require 'xcodeproj'

root = File.expand_path(__dir__)
project_path = File.join(root, 'ReleaseSmoke.xcodeproj')
project = Xcodeproj::Project.new(project_path)
target = project.new_target(:application, 'ReleaseSmoke', :ios, '15.0')

source_group = project.main_group.new_group('ReleaseSmoke', 'ReleaseSmoke')
source = source_group.new_file('ReleaseSmokeApp.swift')
source_group.new_file('Info.plist')
target.add_file_references([source])

target.build_configurations.each do |configuration|
  settings = configuration.build_settings
  settings['CODE_SIGNING_ALLOWED'] = 'NO'
  settings['CODE_SIGNING_REQUIRED'] = 'NO'
  settings['ENABLE_USER_SCRIPT_SANDBOXING'] = 'NO'
  settings['INFOPLIST_FILE'] = 'ReleaseSmoke/Info.plist'
  settings['IPHONEOS_DEPLOYMENT_TARGET'] = '15.0'
  settings['PRODUCT_BUNDLE_IDENTIFIER'] = 'com.wukongim.easysdk.release-smoke'
  settings['SWIFT_VERSION'] = '5.0'
  settings['TARGETED_DEVICE_FAMILY'] = '1,2'
end

project.save

scheme = Xcodeproj::XCScheme.new
scheme.add_build_target(target)
scheme.set_launch_target(target)
scheme.save_as(project_path, 'ReleaseSmoke', true)
