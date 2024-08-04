package scheduler

import (
	"log"
	"math"
	"time"

	"github.com/qiansuo1/cube/node"
	"github.com/qiansuo1/cube/task"
)
const (
	// LIEB square ice constant
	// https://en.wikipedia.org/wiki/Lieb%27s_square_ice_constant
	LIEB = 1.53960071783900203869
)
type Scheduler interface{
    SelectCandidateNodes(t task.Task, nodes []*node.Node) []*node.Node
    Score(t task.Task, nodes []*node.Node) map[string]float64
    Pick(scores map[string]float64, candidates []*node.Node) *node.Node
}

type RoundRobin struct {
	Name       string
	LastWorker int
}

func (r *RoundRobin) SelectCandidateNodes(t task.Task, nodes []*node.Node) []*node.Node {
	return nodes
}

func (r *RoundRobin) Score(t task.Task, nodes []*node.Node) map[string]float64 {
	nodeScores := make(map[string]float64)

	var newWorker int
	if r.LastWorker+1 < len(nodes) {
		newWorker = r.LastWorker + 1
		r.LastWorker++
	} else {
		newWorker = 0
		r.LastWorker = 0
	}

	for idx, node := range nodes {
		if idx == newWorker {
			nodeScores[node.Name] = 0.1
		} else {
			nodeScores[node.Name] = 1.0
		}
	}

	return nodeScores
}

func (r *RoundRobin) Pick(scores map[string]float64, candidates []*node.Node) *node.Node {
	var bestNode *node.Node
	var lowestScore float64
	for idx, node := range candidates {
		if idx == 0 {
			bestNode = node
			lowestScore = scores[node.Name]
			continue
		}

		if scores[node.Name] < lowestScore {
			bestNode = node
			lowestScore = scores[node.Name]
		}
	}

	return bestNode
}


type Epvm struct {
	Name string
}
func (e *Epvm) SelectCandidateNodes(t task.Task, nodes []*node.Node) []*node.Node {
	return selectCandidateNodes(t, nodes)
}

func (e *Epvm) Score(t task.Task, nodes []*node.Node) map[string]float64 {
	nodeScores := make(map[string]float64)
	maxJobs := 4.0
	for _,node := range nodes{
		cpuUsage, err := calculateCpuUsage(node)
		if err != nil {
			log.Printf("error calculating CPU usage for node %s, skipping: %v\n", node.Name, err)
			continue
		}
		cpuLoad := calculateLoad(*cpuUsage, math.Pow(2, 0.8))
		memoryUsedKb := float64(node.Stats.MemUsedKb()) 
		memoryPercentUsedKb := memoryUsedKb  / float64(node.Memory)
		newMemPercent := (calculateLoad(memoryUsedKb+float64(t.Memory/1000), float64(node.Memory)))
	
		memCost := math.Pow(LIEB, newMemPercent) + 
		math.Pow(LIEB, (float64(node.TaskCount+1))/maxJobs) - 
		math.Pow(LIEB, memoryPercentUsedKb) - 
		math.Pow(LIEB, float64(node.TaskCount)/float64(maxJobs))
	
		//cpuload of new task is unknown
		cpuCost := math.Pow(LIEB, cpuLoad) + 
		math.Pow(LIEB, (float64(node.TaskCount+1))/maxJobs) - 
		math.Pow(LIEB, cpuLoad) - 
		math.Pow(LIEB, float64(node.TaskCount)/float64(maxJobs))
		nodeScores[node.Name] = memCost + cpuCost

	}
	return nodeScores

}

func (e *Epvm) Pick(scores map[string]float64, candidates []*node.Node) *node.Node {
	minCost := 0.00
	var bestNode *node.Node
	for idx, node := range candidates {
		if idx == 0 {
			minCost = scores[node.Name]
			bestNode = node
			continue
		}
		if scores[node.Name] < minCost {
			minCost = scores[node.Name]
			bestNode = node
		}
	}
	return bestNode
}

func selectCandidateNodes(t task.Task, nodes []*node.Node) []*node.Node {
	var candidates []*node.Node
	for node := range nodes {
		if checkDisk(t, nodes[node].Disk-nodes[node].DiskAllocated) {
			candidates = append(candidates, nodes[node])
		}
	}
	return candidates
}
func checkDisk(t task.Task, diskAvailable int64) bool {
	return t.Disk <= diskAvailable
}
func calculateLoad(usage float64, capacity float64) float64 {
	return usage / capacity
}
func calculateCpuUsage(node *node.Node) (*float64, error){
	stat1,err := node.GetStats()
	if err != nil {
		return nil, err
	}
	time.Sleep(3 * time.Second)
	stat2, err := node.GetStats()
	if err != nil {
		return nil, err
	}
	//计算两次统计信息中 CPU 处于空闲状态的时间。
	stat1Idle := stat1.CpuStats.Idle + stat1.CpuStats.IOWait
	stat2Idle := stat2.CpuStats.Idle + stat2.CpuStats.IOWait
	//计算两次统计信息中 CPU 处于非空闲状态的时间。
	stat1NonIdle := stat1.CpuStats.User + stat1.CpuStats.Nice + stat1.CpuStats.System + 
	stat1.CpuStats.IRQ + stat1.CpuStats.SoftIRQ + stat1.CpuStats.Steal
	
	stat2NonIdle := stat2.CpuStats.User + stat2.CpuStats.Nice+ 
	stat2.CpuStats.System + stat2.CpuStats.IRQ + stat2.CpuStats.SoftIRQ + stat2.CpuStats.Steal
	
	stat1Total := stat1Idle + stat1NonIdle
	stat2Total := stat2Idle + stat2NonIdle

	total := stat2Total - stat1Total
	idle := stat2Idle - stat1Idle

	var cpuPercentUsage float64

	if total == 0 && idle == 0 {//如果两次统计信息之间CPU的总运行时间差和空闲时间差都为0则CPU使用率为 0%
		cpuPercentUsage = 0.00
		//你在一个时间点记录了 CPU 的状态，记录了所有 CPU 运行时间和空闲时间。
		//然后你在 3 秒钟后再次记录了 CPU 的状态。如果两次记录结果完全相同，这意味着这 3 秒钟内
		//CPU 没有进行任何操作，也没有进入空闲状态，所以它的使用率就是 0%
	} else {
		cpuPercentUsage = (float64(total) - float64(idle)) / float64(total)
	}
	return &cpuPercentUsage, nil
}

type Greedy struct {
	Name string
}

func (g *Greedy) SelectCandidateNodes(t task.Task, nodes []*node.Node) []*node.Node {
	return selectCandidateNodes(t, nodes)
}

func (g *Greedy) Score(t task.Task, nodes []*node.Node) map[string]float64 {
	nodeScores := make(map[string]float64)

	for _, node := range nodes {
		cpuUsage, err := calculateCpuUsage(node)
		if err != nil {
			log.Printf("error calculating CPU usage for node %s, skipping: %v\n", node.Name, err)
			continue
		}
		cpuLoad := calculateLoad(float64(*cpuUsage), math.Pow(2, 0.8))
		nodeScores[node.Name] = cpuLoad
	}
	return nodeScores
}

func (g *Greedy) Pick(candidates map[string]float64, nodes []*node.Node) *node.Node {
	minCpu := 0.00
	var bestNode *node.Node
	for idx, node := range nodes {
		if idx == 0 {
			minCpu = candidates[node.Name]
			bestNode = node
			continue
		}
		if candidates[node.Name] < minCpu {
			minCpu = candidates[node.Name]
			bestNode = node
		}
	}
	return bestNode
}
