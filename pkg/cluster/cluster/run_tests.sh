#!/bin/bash

# 发送队列优化测试套件运行脚本

set -e

echo "🚀 Running Send Queue Optimization Test Suite"
echo "=============================================="

# 设置测试环境
export GO111MODULE=on
export CGO_ENABLED=0

# 测试目录
TEST_DIR="./pkg/cluster/cluster"

echo ""
echo "📋 Test Plan:"
echo "  1. Configuration Tests"
echo "  2. Adaptive Queue Unit Tests"
echo "  3. Improved Node Unit Tests"
echo "  4. Integration Tests"
echo "  5. Stress Tests (optional)"
echo "  6. Benchmark Tests"
echo ""

# 1. 配置测试
echo "🔧 Running Configuration Tests..."
go test -v -run "TestDefaultSendQueueConfig|TestHighThroughputConfig|TestLowLatencyConfig|TestMemoryConstrainedConfig|TestAdaptiveConfig|TestGetConfigByProfile|TestConfigValidation|TestApplyToOptions|TestAnalyzePerformance|TestConfigTemplates" $TEST_DIR

if [ $? -eq 0 ]; then
    echo "✅ Configuration tests passed"
else
    echo "❌ Configuration tests failed"
    exit 1
fi

echo ""

# 2. 自适应队列单元测试
echo "🔄 Running Adaptive Queue Unit Tests..."
go test -v -run "TestAdaptiveSendQueue_BasicOperations|TestAdaptiveSendQueue_AutoExpansion|TestAdaptiveSendQueue_PriorityQueue|TestAdaptiveSendQueue_BatchReceive|TestAdaptiveSendQueue_RateLimiting|TestAdaptiveSendQueue_QueueFull|TestAdaptiveSendQueue_Shrinking|TestAdaptiveSendQueue_Stats" $TEST_DIR

if [ $? -eq 0 ]; then
    echo "✅ Adaptive queue unit tests passed"
else
    echo "❌ Adaptive queue unit tests failed"
    exit 1
fi

echo ""

# 3. 改进节点单元测试
echo "🌐 Running Improved Node Unit Tests..."
go test -v -run "TestImprovedNode_BasicSend|TestImprovedNode_PrioritySend|TestImprovedNode_SendStrategy|TestImprovedNode_Backpressure|TestImprovedNode_QueueExpansion|TestImprovedNode_PerformanceMonitoring" $TEST_DIR

if [ $? -eq 0 ]; then
    echo "✅ Improved node unit tests passed"
else
    echo "❌ Improved node unit tests failed"
    exit 1
fi

echo ""

# 4. 集成测试
echo "🔗 Running Integration Tests..."
go test -v -run "TestSendQueueIntegration|TestConfigurationProfiles|TestPerformanceAnalysis" $TEST_DIR

if [ $? -eq 0 ]; then
    echo "✅ Integration tests passed"
else
    echo "❌ Integration tests failed"
    exit 1
fi

echo ""

# 5. 压力测试（可选）
if [ "$1" = "--stress" ] || [ "$1" = "-s" ]; then
    echo "💪 Running Stress Tests..."
    echo "⚠️  This may take several minutes..."
    
    go test -v -run "TestAdaptiveQueue_HighThroughputStress|TestAdaptiveQueue_LowLatencyStress|TestAdaptiveQueue_MemoryConstrainedStress|TestImprovedNode_ConcurrentStress|TestAdaptiveQueue_BackpressureStress" $TEST_DIR -timeout=10m

    if [ $? -eq 0 ]; then
        echo "✅ Stress tests passed"
    else
        echo "❌ Stress tests failed"
        exit 1
    fi
    echo ""
fi

# 6. 基准测试
echo "📊 Running Benchmark Tests..."
go test -bench=. -run=^$ $TEST_DIR -benchtime=3s

if [ $? -eq 0 ]; then
    echo "✅ Benchmark tests completed"
else
    echo "❌ Benchmark tests failed"
    exit 1
fi

echo ""
echo "🎉 All tests completed successfully!"
echo ""

# 生成测试报告
echo "📈 Generating Test Report..."
go test -v -coverprofile=coverage.out $TEST_DIR > test_report.txt 2>&1

if [ -f coverage.out ]; then
    COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}')
    echo "📊 Test Coverage: $COVERAGE"
    
    # 生成HTML覆盖率报告
    go tool cover -html=coverage.out -o coverage.html
    echo "📄 Coverage report generated: coverage.html"
fi

echo ""
echo "📋 Test Summary:"
echo "  ✅ Configuration Tests"
echo "  ✅ Adaptive Queue Unit Tests"
echo "  ✅ Improved Node Unit Tests"
echo "  ✅ Integration Tests"
if [ "$1" = "--stress" ] || [ "$1" = "-s" ]; then
    echo "  ✅ Stress Tests"
fi
echo "  ✅ Benchmark Tests"
echo ""
echo "🚀 Send Queue Optimization is ready for deployment!"

# 清理临时文件
rm -f coverage.out test_report.txt

echo ""
echo "Usage:"
echo "  ./run_tests.sh           # Run basic test suite"
echo "  ./run_tests.sh --stress  # Run with stress tests"
echo "  ./run_tests.sh -s        # Run with stress tests (short)"
