package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DockerStat represents a single Docker container statistics entry
type DockerStat struct {
	BlockIO   string `json:"BlockIO"`
	CPUPerc   string `json:"CPUPerc"`
	Container string `json:"Container"`
	ID        string `json:"ID"`
	MemPerc   string `json:"MemPerc"`
	MemUsage  string `json:"MemUsage"`
	Name      string `json:"Name"`
	NetIO     string `json:"NetIO"`
	PIDs      string `json:"PIDs"`
}

// StatsFile represents a stats file with its data
type StatsFile struct {
	Name      string
	Timestamp time.Time
	Stats     []DockerStat
}

// ServerData holds all parsed stats files
type ServerData struct {
	Files []StatsFile
}

// ContainerComparison holds historical data for a container
type ContainerComparison struct {
	ContainerID   string               `json:"container_id"`
	ContainerName string               `json:"container_name"`
	Data          []ContainerDataPoint `json:"data"`
}

// ContainerComparisonWithStats extends ContainerComparison with calculated statistics
type ContainerComparisonWithStats struct {
	ContainerComparison
	AvgCPU float64
	MaxCPU float64
	MinCPU float64
	AvgMem float64
	MaxMem float64
	MinMem float64
}

// ContainerSummary holds aggregated statistics for a container across all files
type ContainerSummary struct {
	ContainerID   string  `json:"container_id"`
	ContainerName string  `json:"container_name"`
	DataPoints    int     `json:"data_points"`
	AvgCPU        float64 `json:"avg_cpu"`
	MaxCPU        float64 `json:"max_cpu"`
	MinCPU        float64 `json:"min_cpu"`
	AvgMem        float64 `json:"avg_mem"`
	MaxMem        float64 `json:"max_mem"`
	MinMem        float64 `json:"min_mem"`
	FirstSeen     string  `json:"first_seen"`
	LastSeen      string  `json:"last_seen"`
}

// ContainerDataPoint represents a single data point for a container
type ContainerDataPoint struct {
	Timestamp string  `json:"timestamp"`
	CPUPerc   float64 `json:"cpu_perc"`
	MemPerc   float64 `json:"mem_perc"`
	MemUsage  string  `json:"mem_usage"`
	NetIO     string  `json:"net_io"`
	BlockIO   string  `json:"block_io"`
	PIDs      string  `json:"pids"`
}

// parseStatsFile parses a single stats JSON file
func parseStatsFile(filePath string) (StatsFile, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return StatsFile{}, fmt.Errorf("error opening file %s: %v", filePath, err)
	}
	defer file.Close()

	var dockerStats []DockerStat
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			continue
		}

		var stat DockerStat
		if err := json.Unmarshal([]byte(line), &stat); err != nil {
			return StatsFile{}, fmt.Errorf("error parsing line %d in %s: %v", lineNum, filePath, err)
		}

		dockerStats = append(dockerStats, stat)
	}

	if err := scanner.Err(); err != nil {
		return StatsFile{}, fmt.Errorf("error reading file %s: %v", filePath, err)
	}

	// Extract timestamp from filename
	basename := filepath.Base(filePath)
	timestamp := time.Now() // fallback
	if strings.Contains(basename, "_") {
		parts := strings.Split(basename, "_")
		if len(parts) >= 3 {
			dateStr := parts[0] + "_" + parts[1]
			if t, err := time.Parse("2006-01-02_15-04-05", dateStr); err == nil {
				timestamp = t
			}
		}
	}

	return StatsFile{
		Name:      basename,
		Timestamp: timestamp,
		Stats:     dockerStats,
	}, nil
}

// loadAllStatsFiles loads and parses all JSON files from the stats directory
func loadAllStatsFiles(dir string) ([]StatsFile, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("error reading directory %s: %v", dir, err)
	}

	var statsFiles []StatsFile
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(dir, file.Name())
		statsFile, err := parseStatsFile(filePath)
		if err != nil {
			log.Printf("Warning: failed to parse %s: %v", filePath, err)
			continue
		}

		statsFiles = append(statsFiles, statsFile)
	}

	// Sort by timestamp (newest first)
	sort.Slice(statsFiles, func(i, j int) bool {
		return statsFiles[i].Timestamp.After(statsFiles[j].Timestamp)
	})

	return statsFiles, nil
}

// getContainerComparison returns historical data for a specific container
func getContainerComparison(statsFiles []StatsFile, containerID string) ContainerComparison {
	var dataPoints []ContainerDataPoint
	var containerName string

	for _, statsFile := range statsFiles {
		for _, stat := range statsFile.Stats {
			if stat.ID == containerID {
				// Parse CPU percentage
				cpuStr := strings.TrimSuffix(stat.CPUPerc, "%")
				cpuPerc, _ := strconv.ParseFloat(cpuStr, 64)

				// Parse Memory percentage
				memStr := strings.TrimSuffix(stat.MemPerc, "%")
				memPerc, _ := strconv.ParseFloat(memStr, 64)

				dataPoint := ContainerDataPoint{
					Timestamp: statsFile.Timestamp.Format("2006-01-02 15:04:05"),
					CPUPerc:   cpuPerc,
					MemPerc:   memPerc,
					MemUsage:  stat.MemUsage,
					NetIO:     stat.NetIO,
					BlockIO:   stat.BlockIO,
					PIDs:      stat.PIDs,
				}
				dataPoints = append(dataPoints, dataPoint)

				if containerName == "" {
					containerName = stat.Name
				}
			}
		}
	}

	// Sort data points by timestamp (oldest first for proper timeline)
	sort.Slice(dataPoints, func(i, j int) bool {
		t1, _ := time.Parse("2006-01-02 15:04:05", dataPoints[i].Timestamp)
		t2, _ := time.Parse("2006-01-02 15:04:05", dataPoints[j].Timestamp)
		return t1.Before(t2)
	})

	return ContainerComparison{
		ContainerID:   containerID,
		ContainerName: containerName,
		Data:          dataPoints,
	}
}

// getContainerComparisonWithStats returns historical data with calculated statistics
func getContainerComparisonWithStats(statsFiles []StatsFile, containerID string) ContainerComparisonWithStats {
	comparison := getContainerComparison(statsFiles, containerID)

	if len(comparison.Data) == 0 {
		return ContainerComparisonWithStats{
			ContainerComparison: comparison,
		}
	}

	// Calculate statistics
	var cpuValues, memValues []float64
	for _, point := range comparison.Data {
		cpuValues = append(cpuValues, point.CPUPerc)
		memValues = append(memValues, point.MemPerc)
	}

	// Calculate CPU stats
	var cpuSum float64
	maxCPU := cpuValues[0]
	minCPU := cpuValues[0]
	for _, cpu := range cpuValues {
		cpuSum += cpu
		if cpu > maxCPU {
			maxCPU = cpu
		}
		if cpu < minCPU {
			minCPU = cpu
		}
	}
	avgCPU := cpuSum / float64(len(cpuValues))

	// Calculate Memory stats
	var memSum float64
	maxMem := memValues[0]
	minMem := memValues[0]
	for _, mem := range memValues {
		memSum += mem
		if mem > maxMem {
			maxMem = mem
		}
		if mem < minMem {
			minMem = mem
		}
	}
	avgMem := memSum / float64(len(memValues))

	return ContainerComparisonWithStats{
		ContainerComparison: comparison,
		AvgCPU:              avgCPU,
		MaxCPU:              maxCPU,
		MinCPU:              minCPU,
		AvgMem:              avgMem,
		MaxMem:              maxMem,
		MinMem:              minMem,
	}
}

// getAllContainerSummaries returns aggregated statistics for all containers across all files
func getAllContainerSummaries(statsFiles []StatsFile) []ContainerSummary {
	containerData := make(map[string][]ContainerDataPoint)
	containerNames := make(map[string]string)

	// Collect all data points for each container
	for _, statsFile := range statsFiles {
		for _, stat := range statsFile.Stats {
			// Parse CPU percentage
			cpuStr := strings.TrimSuffix(stat.CPUPerc, "%")
			cpuPerc, _ := strconv.ParseFloat(cpuStr, 64)

			// Parse Memory percentage
			memStr := strings.TrimSuffix(stat.MemPerc, "%")
			memPerc, _ := strconv.ParseFloat(memStr, 64)

			dataPoint := ContainerDataPoint{
				Timestamp: statsFile.Timestamp.Format("2006-01-02 15:04:05"),
				CPUPerc:   cpuPerc,
				MemPerc:   memPerc,
				MemUsage:  stat.MemUsage,
				NetIO:     stat.NetIO,
				BlockIO:   stat.BlockIO,
				PIDs:      stat.PIDs,
			}

			containerData[stat.ID] = append(containerData[stat.ID], dataPoint)
			containerNames[stat.ID] = stat.Name
		}
	}

	// Calculate statistics for each container
	var summaries []ContainerSummary
	for containerID, dataPoints := range containerData {
		if len(dataPoints) == 0 {
			continue
		}

		// Sort data points by timestamp
		sort.Slice(dataPoints, func(i, j int) bool {
			t1, _ := time.Parse("2006-01-02 15:04:05", dataPoints[i].Timestamp)
			t2, _ := time.Parse("2006-01-02 15:04:05", dataPoints[j].Timestamp)
			return t1.Before(t2)
		})

		// Calculate CPU statistics
		var cpuSum float64
		maxCPU := dataPoints[0].CPUPerc
		minCPU := dataPoints[0].CPUPerc
		for _, point := range dataPoints {
			cpuSum += point.CPUPerc
			if point.CPUPerc > maxCPU {
				maxCPU = point.CPUPerc
			}
			if point.CPUPerc < minCPU {
				minCPU = point.CPUPerc
			}
		}
		avgCPU := cpuSum / float64(len(dataPoints))

		// Calculate Memory statistics
		var memSum float64
		maxMem := dataPoints[0].MemPerc
		minMem := dataPoints[0].MemPerc
		for _, point := range dataPoints {
			memSum += point.MemPerc
			if point.MemPerc > maxMem {
				maxMem = point.MemPerc
			}
			if point.MemPerc < minMem {
				minMem = point.MemPerc
			}
		}
		avgMem := memSum / float64(len(dataPoints))

		summary := ContainerSummary{
			ContainerID:   containerID,
			ContainerName: containerNames[containerID],
			DataPoints:    len(dataPoints),
			AvgCPU:        avgCPU,
			MaxCPU:        maxCPU,
			MinCPU:        minCPU,
			AvgMem:        avgMem,
			MaxMem:        maxMem,
			MinMem:        minMem,
			FirstSeen:     dataPoints[0].Timestamp,
			LastSeen:      dataPoints[len(dataPoints)-1].Timestamp,
		}

		summaries = append(summaries, summary)
	}

	// Sort by average CPU usage (descending)
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].AvgCPU > summaries[j].AvgCPU
	})

	return summaries
}

const htmlTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Docker Stats Viewer</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        table { border-collapse: collapse; width: 100%; margin-top: 20px; }
        th, td { border: 1px solid #ddd; padding: 8px; text-align: left; }
        th { background-color: #f2f2f2; cursor: pointer; user-select: none; }
        th:hover { background-color: #e6e6e6; }
        select { padding: 5px; margin: 10px 0; }
        .stats-summary { background: #f9f9f9; padding: 15px; margin: 10px 0; border-radius: 5px; }
        .high-usage { background-color: #ffe6e6; }
        .medium-usage { background-color: #fff3cd; }
        
        /* Modal styles */
        .modal {
            display: none;
            position: fixed;
            z-index: 1000;
            left: 0;
            top: 0;
            width: 100%;
            height: 100%;
            background-color: #fff;
            overflow-y: auto;
        }
        .modal-content {
            background-color: #fefefe;
            margin: 0;
            padding: 20px;
            width: 100%;
            height: 100%;
            position: relative;
            box-sizing: border-box;
        }
        .close {
            color: #aaa;
            float: right;
            font-size: 28px;
            font-weight: bold;
            cursor: pointer;
            position: fixed;
            right: 25px;
            top: 20px;
            background: #fff;
            border: 2px solid #ddd;
            border-radius: 50%;
            width: 40px;
            height: 40px;
            display: flex;
            align-items: center;
            justify-content: center;
            z-index: 1001;
        }
        .close:hover,
        .close:focus {
            color: black;
        }
        .clickable-id {
            color: #007bff;
            cursor: pointer;
            text-decoration: underline;
        }
        .clickable-id:hover {
            color: #0056b3;
        }
        .loading {
            text-align: center;
            padding: 20px;
        }
        .comparison-table {
            width: 100%;
            margin-top: 10px;
            font-size: 14px;
        }
        .comparison-table th {
            background-color: #e9ecef;
            position: sticky;
            top: 0;
            z-index: 10;
        }
        .comparison-table td {
            padding: 6px 8px;
        }
        .metric-high { color: #dc3545; font-weight: bold; }
        .metric-medium { color: #fd7e14; }
        .metric-low { color: #28a745; }
    </style>
</head>
<body>
    <h1>Docker Stats Viewer</h1>
    
    <div style="margin-bottom: 20px;">
        <a href="/summary" style="background: #007bff; color: white; padding: 10px 20px; text-decoration: none; border-radius: 5px; margin-right: 10px;">View Summary Report</a>
    </div>
    
    <div class="stats-summary">
        <h3>File: {{.SelectedFile.Name}}</h3>
        <p>Timestamp: {{.SelectedFile.Timestamp.Format "2006-01-02 15:04:05"}}</p>
        <p>Total containers: {{len .SelectedFile.Stats}}</p>
    </div>

    <form method="GET">
        <label for="file">Select stats file:</label>
        <select name="file" id="file" onchange="this.form.submit()">
            {{range $i, $file := .Files}}
            <option value="{{$i}}" {{if eq $i $.SelectedIndex}}selected{{end}}>
                {{$file.Name}} ({{$file.Timestamp.Format "2006-01-02 15:04:05"}})
            </option>
            {{end}}
        </select>
    </form>

    <div style="margin: 10px 0;">
        <label for="searchInput">Search by container name:</label>
        <input type="text" id="searchInput" placeholder="Enter container name..." onkeyup="filterTable()" style="padding: 5px; margin-left: 10px; width: 250px;">
        <button onclick="clearSearch()" style="margin-left: 5px; padding: 5px 10px;">Clear</button>
    </div>

    <table id="statsTable">
        <thead>
            <tr>
                <th onclick="sortTable(0)">Container Name</th>
                <th onclick="sortTable(1)">ID</th>
                <th onclick="sortTable(2)">CPU %</th>
                <th onclick="sortTable(3)">Memory %</th>
                <th onclick="sortTable(4)">Memory Usage</th>
                <th onclick="sortTable(5)">Network I/O</th>
                <th onclick="sortTable(6)">Block I/O</th>
                <th onclick="sortTable(7)">PIDs</th>
            </tr>
        </thead>
        <tbody>
            {{range .SelectedFile.Stats}}
            <tr class="{{if gt (parseFloat .MemPerc) 80.0}}high-usage{{else if gt (parseFloat .MemPerc) 50.0}}medium-usage{{end}}">
                <td>{{.Name}}</td>
                <td><a href="/container/{{.ID}}" class="clickable-id">{{.ID}}</a></td>
                <td>{{.CPUPerc}}</td>
                <td>{{.MemPerc}}</td>
                <td>{{.MemUsage}}</td>
                <td>{{.NetIO}}</td>
                <td>{{.BlockIO}}</td>
                <td>{{.PIDs}}</td>
            </tr>
            {{end}}
        </tbody>
    </table>

    <!-- Modal -->
    <div id="comparisonModal" class="modal">
        <div class="modal-content">
            <span class="close" onclick="closeModal()">&times;</span>
            <div id="modalContent">
                <div class="loading">Loading...</div>
            </div>
        </div>
    </div>

    <script>
        let sortDirection = {};
        
        function sortTable(columnIndex) {
            const table = document.getElementById('statsTable');
            const tbody = table.querySelector('tbody');
            const rows = Array.from(tbody.querySelectorAll('tr')).filter(row => row.style.display !== 'none');
            
            const isNumeric = (str) => {
                if (columnIndex === 2 || columnIndex === 3) { // CPU % or Memory %
                    return !isNaN(parseFloat(str.replace('%', '')));
                }
                return !isNaN(parseFloat(str));
            };
            
            const getValue = (row, index) => {
                let value = row.cells[index].textContent.trim();
                if (columnIndex === 2 || columnIndex === 3) {
                    return parseFloat(value.replace('%', '')) || 0;
                }
                return isNumeric(value) ? parseFloat(value) : value.toLowerCase();
            };
            
            const currentDirection = sortDirection[columnIndex] || 'asc';
            const newDirection = currentDirection === 'asc' ? 'desc' : 'asc';
            sortDirection[columnIndex] = newDirection;
            
            rows.sort((a, b) => {
                const aVal = getValue(a, columnIndex);
                const bVal = getValue(b, columnIndex);
                
                let comparison = 0;
                if (aVal < bVal) comparison = -1;
                else if (aVal > bVal) comparison = 1;
                
                return newDirection === 'asc' ? comparison : -comparison;
            });
            
            // Reorder rows
            rows.forEach(row => tbody.appendChild(row));
        }

        function filterTable() {
            const input = document.getElementById('searchInput');
            const filter = input.value.toLowerCase();
            const table = document.getElementById('statsTable');
            const tbody = table.querySelector('tbody');
            const rows = tbody.querySelectorAll('tr');
            
            rows.forEach(row => {
                const containerName = row.cells[0].textContent.toLowerCase();
                if (containerName.includes(filter)) {
                    row.style.display = '';
                } else {
                    row.style.display = 'none';
                }
            });
        }

        function clearSearch() {
            document.getElementById('searchInput').value = '';
            filterTable();
        }

        function openModal(containerId) {
            const modal = document.getElementById('comparisonModal');
            const modalContent = document.getElementById('modalContent');
            
            modal.style.display = 'block';
            modalContent.innerHTML = '<div class="loading">Loading comparison data...</div>';
            
            // Fetch comparison data
            fetch('/api/container/' + containerId)
                .then(response => response.json())
                .then(data => {
                    displayComparisonData(data);
                })
                .catch(error => {
                    modalContent.innerHTML = '<div style="color: red;">Error loading data: ' + error.message + '</div>';
                });
        }

        function closeModal() {
            document.getElementById('comparisonModal').style.display = 'none';
        }

        function displayComparisonData(data) {
            const modalContent = document.getElementById('modalContent');
            
            if (!data.data || data.data.length === 0) {
                modalContent.innerHTML = '<h3>No historical data found for container: ' + data.container_id + '</h3>';
                return;
            }

            let html = '<h1>Container Historical Analysis</h1>';
            html += '<div style="background: #f8f9fa; padding: 15px; border-radius: 5px; margin-bottom: 20px;">';
            html += '<h3>Container Information</h3>';
            html += '<p><strong>Container Name:</strong> ' + (data.container_name || 'Unknown') + '</p>';
            html += '<p><strong>Container ID:</strong> ' + data.container_id + '</p>';
            html += '<p><strong>Total Data Points:</strong> ' + data.data.length + '</p>';
            html += '</div>';

            html += '<table class="comparison-table">';
            html += '<thead><tr>';
            html += '<th>Timestamp</th>';
            html += '<th>CPU %</th>';
            html += '<th>Memory %</th>';
            html += '<th>Memory Usage</th>';
            html += '<th>Network I/O</th>';
            html += '<th>Block I/O</th>';
            html += '<th>PIDs</th>';
            html += '</tr></thead>';
            html += '<tbody>';

            data.data.forEach(point => {
                const cpuClass = point.cpu_perc > 80 ? 'metric-high' : point.cpu_perc > 50 ? 'metric-medium' : 'metric-low';
                const memClass = point.mem_perc > 80 ? 'metric-high' : point.mem_perc > 50 ? 'metric-medium' : 'metric-low';
                
                html += '<tr>';
                html += '<td>' + point.timestamp + '</td>';
                html += '<td class="' + cpuClass + '">' + point.cpu_perc.toFixed(2) + '%</td>';
                html += '<td class="' + memClass + '">' + point.mem_perc.toFixed(2) + '%</td>';
                html += '<td>' + (point.mem_usage || 'N/A') + '</td>';
                html += '<td>' + (point.net_io || 'N/A') + '</td>';
                html += '<td>' + (point.block_io || 'N/A') + '</td>';
                html += '<td>' + (point.pids || 'N/A') + '</td>';
                html += '</tr>';
            });

            html += '</tbody></table>';

            // Calculate averages
            const avgCpu = data.data.reduce((sum, point) => sum + point.cpu_perc, 0) / data.data.length;
            const avgMem = data.data.reduce((sum, point) => sum + point.mem_perc, 0) / data.data.length;
            const maxCpu = Math.max(...data.data.map(point => point.cpu_perc));
            const maxMem = Math.max(...data.data.map(point => point.mem_perc));
            const minCpu = Math.min(...data.data.map(point => point.cpu_perc));
            const minMem = Math.min(...data.data.map(point => point.mem_perc));

            html += '<div style="margin-top: 30px; background: #f8f9fa; padding: 20px; border-radius: 5px;">';
            html += '<h3>Summary Statistics</h3>';
            html += '<div style="display: grid; grid-template-columns: repeat(2, 1fr); gap: 20px;">';
            html += '<div>';
            html += '<h4>CPU Usage</h4>';
            html += '<p><strong>Average:</strong> ' + avgCpu.toFixed(2) + '%</p>';
            html += '<p><strong>Peak:</strong> ' + maxCpu.toFixed(2) + '%</p>';
            html += '<p><strong>Minimum:</strong> ' + minCpu.toFixed(2) + '%</p>';
            html += '</div>';
            html += '<div>';
            html += '<h4>Memory Usage</h4>';
            html += '<p><strong>Average:</strong> ' + avgMem.toFixed(2) + '%</p>';
            html += '<p><strong>Peak:</strong> ' + maxMem.toFixed(2) + '%</p>';
            html += '<p><strong>Minimum:</strong> ' + minMem.toFixed(2) + '%</p>';
            html += '</div>';
            html += '</div>';
            html += '</div>';

            modalContent.innerHTML = html;
        }

        // Close modal with Escape key
        document.addEventListener('keydown', function(event) {
            if (event.key === 'Escape') {
                closeModal();
            }
        });
    </script>
</body>
</html>
`

const containerPageTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Container Details - {{.ContainerName}}</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        .back-link { 
            display: inline-block; 
            margin-bottom: 20px; 
            color: #007bff; 
            text-decoration: none; 
            padding: 8px 15px;
            border: 1px solid #007bff;
            border-radius: 4px;
        }
        .back-link:hover { 
            background-color: #007bff; 
            color: white; 
        }
        .container-info { 
            background: #f8f9fa; 
            padding: 20px; 
            border-radius: 5px; 
            margin-bottom: 30px; 
        }
        .stats-grid { 
            display: grid; 
            grid-template-columns: repeat(2, 1fr); 
            gap: 30px; 
            margin-bottom: 30px; 
        }
        .stats-card { 
            background: #f8f9fa; 
            padding: 20px; 
            border-radius: 5px; 
        }
        table { 
            border-collapse: collapse; 
            width: 100%; 
            margin-top: 20px; 
        }
        th, td { 
            border: 1px solid #ddd; 
            padding: 8px; 
            text-align: left; 
        }
        th { 
            background-color: #e9ecef; 
            position: sticky; 
            top: 0; 
            z-index: 10; 
        }
        .metric-high { color: #dc3545; font-weight: bold; }
        .metric-medium { color: #fd7e14; }
        .metric-low { color: #28a745; }
        .no-data {
            text-align: center;
            padding: 40px;
            color: #6c757d;
            background: #f8f9fa;
            border-radius: 5px;
        }
    </style>
</head>
<body>
    <a href="/" class="back-link"><- Back to Dashboard</a>
    
    <h1>Container Historical Analysis</h1>
    
    {{if .Data}}
    <div class="container-info">
        <h2>Container Information</h2>
        <p><strong>Container Name:</strong> {{.ContainerName}}</p>
        <p><strong>Container ID:</strong> {{.ContainerID}}</p>
        <p><strong>Total Data Points:</strong> {{len .Data}}</p>
        <p><strong>Data Range:</strong> {{(index .Data 0).Timestamp}} to {{(index .Data (sub (len .Data) 1)).Timestamp}}</p>
    </div>

    <div class="stats-grid">
        <div class="stats-card">
            <h3>CPU Usage Statistics</h3>
            <p><strong>Average:</strong> {{printf "%.2f" .AvgCPU}}%</p>
            <p><strong>Peak:</strong> {{printf "%.2f" .MaxCPU}}%</p>
            <p><strong>Minimum:</strong> {{printf "%.2f" .MinCPU}}%</p>
        </div>
        <div class="stats-card">
            <h3>Memory Usage Statistics</h3>
            <p><strong>Average:</strong> {{printf "%.2f" .AvgMem}}%</p>
            <p><strong>Peak:</strong> {{printf "%.2f" .MaxMem}}%</p>
            <p><strong>Minimum:</strong> {{printf "%.2f" .MinMem}}%</p>
        </div>
    </div>

    <h2>Historical Data</h2>
    <table>
        <thead>
            <tr>
                <th>Timestamp</th>
                <th>CPU %</th>
                <th>Memory %</th>
                <th>Memory Usage</th>
                <th>Network I/O</th>
                <th>Block I/O</th>
                <th>PIDs</th>
            </tr>
        </thead>
        <tbody>
            {{range .Data}}
            <tr>
                <td>{{.Timestamp}}</td>
                <td class="{{if gt .CPUPerc 80.0}}metric-high{{else if gt .CPUPerc 50.0}}metric-medium{{else}}metric-low{{end}}">{{printf "%.2f" .CPUPerc}}%</td>
                <td class="{{if gt .MemPerc 80.0}}metric-high{{else if gt .MemPerc 50.0}}metric-medium{{else}}metric-low{{end}}">{{printf "%.2f" .MemPerc}}%</td>
                <td>{{.MemUsage}}</td>
                <td>{{.NetIO}}</td>
                <td>{{.BlockIO}}</td>
                <td>{{.PIDs}}</td>
            </tr>
            {{end}}
        </tbody>
    </table>
    {{else}}
    <div class="no-data">
        <h3>No Historical Data Found</h3>
        <p>No data points were found for container ID: <strong>{{.ContainerID}}</strong></p>
        <p>This could mean the container was recently created or the statistics haven't been collected yet.</p>
    </div>
    {{end}}
</body>
</html>
`

const summaryPageTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Container Summary - All Files Analysis</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        .back-link { 
            display: inline-block; 
            margin-bottom: 20px; 
            color: #007bff; 
            text-decoration: none; 
            padding: 8px 15px;
            border: 1px solid #007bff;
            border-radius: 4px;
        }
        .back-link:hover { 
            background-color: #007bff; 
            color: white; 
        }
        .summary-info { 
            background: #f8f9fa; 
            padding: 20px; 
            border-radius: 5px; 
            margin-bottom: 30px; 
        }
        table { 
            border-collapse: collapse; 
            width: 100%; 
            margin-top: 20px; 
        }
        th, td { 
            border: 1px solid #ddd; 
            padding: 8px; 
            text-align: left; 
        }
        th { 
            background-color: #f2f2f2; 
            cursor: pointer; 
            user-select: none;
            position: sticky; 
            top: 0; 
            z-index: 10; 
        }
        th:hover { 
            background-color: #e6e6e6; 
        }
        .metric-high { 
            background-color: #f8d7da; 
            color: #721c24; 
            font-weight: bold; 
        }
        .metric-medium { 
            background-color: #fff3cd; 
            color: #856404; 
        }
        .metric-low { 
            background-color: #d1ecf1; 
            color: #0c5460; 
        }
        .clickable-id {
            color: #007bff;
            cursor: pointer;
            text-decoration: underline;
        }
        .clickable-id:hover {
            color: #0056b3;
        }
        .search-container {
            margin: 10px 0;
        }
        .search-container input {
            padding: 5px;
            margin-left: 10px;
            width: 250px;
        }
        .search-container button {
            margin-left: 5px;
            padding: 5px 10px;
        }
        .stats-summary {
            display: grid;
            grid-template-columns: repeat(3, 1fr);
            gap: 20px;
            margin-bottom: 20px;
        }
        .stats-card {
            background: #f8f9fa;
            padding: 15px;
            border-radius: 5px;
            text-align: center;
        }
        .stats-card h3 {
            margin-top: 0;
            color: #495057;
        }
        .stats-value {
            font-size: 24px;
            font-weight: bold;
            color: #007bff;
        }
    </style>
</head>
<body>
    <a href="/" class="back-link"><- Back to Dashboard</a>
    
    <h1>Container Summary - All Files Analysis</h1>
    
    <div class="summary-info">
        <h3>Analysis Overview</h3>
        <p><strong>Total Files Analyzed:</strong> {{.TotalFiles}}</p>
        <p><strong>Total Containers:</strong> {{len .Summaries}}</p>
        <p><strong>Analysis Period:</strong> {{.FirstTimestamp}} to {{.LastTimestamp}}</p>
    </div>

    <div class="stats-summary">
        <div class="stats-card">
            <h3>Highest Avg CPU</h3>
            <div class="stats-value">{{if .Summaries}}{{printf "%.1f%%" (index .Summaries 0).AvgCPU}}{{else}}N/A{{end}}</div>
            <p>{{if .Summaries}}{{(index .Summaries 0).ContainerName}}{{end}}</p>
        </div>
        <div class="stats-card">
            <h3>Highest Peak CPU</h3>
            <div class="stats-value">{{if .HighestPeakCPU}}{{printf "%.1f%%" .HighestPeakCPU.MaxCPU}}{{else}}N/A{{end}}</div>
            <p>{{if .HighestPeakCPU}}{{.HighestPeakCPU.ContainerName}}{{end}}</p>
        </div>
        <div class="stats-card">
            <h3>Most Data Points</h3>
            <div class="stats-value">{{if .MostDataPoints}}{{.MostDataPoints.DataPoints}}{{else}}N/A{{end}}</div>
            <p>{{if .MostDataPoints}}{{.MostDataPoints.ContainerName}}{{end}}</p>
        </div>
    </div>

    <div class="search-container">
        <label for="searchInput">Search by container name:</label>
        <input type="text" id="searchInput" placeholder="Enter container name..." onkeyup="filterTable()">
        <button onclick="clearSearch()">Clear</button>
    </div>

    <table id="summaryTable">
        <thead>
            <tr>
                <th onclick="sortTable(0)">Container Name</th>
                <th onclick="sortTable(1)">ID</th>
                <th onclick="sortTable(2)">Data Points</th>
                <th onclick="sortTable(3)">Avg CPU %</th>
                <th onclick="sortTable(4)">Peak CPU %</th>
                <th onclick="sortTable(5)">Min CPU %</th>
                <th onclick="sortTable(6)">Avg Mem %</th>
                <th onclick="sortTable(7)">Peak Mem %</th>
                <th onclick="sortTable(8)">Min Mem %</th>
                <th onclick="sortTable(9)">First Seen</th>
                <th onclick="sortTable(10)">Last Seen</th>
            </tr>
        </thead>
        <tbody>
            {{range .Summaries}}
            <tr>
                <td>{{.ContainerName}}</td>
                <td><a href="/container/{{.ContainerID}}" class="clickable-id">{{.ContainerID}}</a></td>
                <td>{{.DataPoints}}</td>
                <td class="{{if gt .AvgCPU 80.0}}metric-high{{else if gt .AvgCPU 50.0}}metric-medium{{else}}metric-low{{end}}">{{printf "%.2f" .AvgCPU}}%</td>
                <td class="{{if gt .MaxCPU 90.0}}metric-high{{else if gt .MaxCPU 70.0}}metric-medium{{else}}metric-low{{end}}">{{printf "%.2f" .MaxCPU}}%</td>
                <td>{{printf "%.2f" .MinCPU}}%</td>
                <td class="{{if gt .AvgMem 80.0}}metric-high{{else if gt .AvgMem 50.0}}metric-medium{{else}}metric-low{{end}}">{{printf "%.2f" .AvgMem}}%</td>
                <td class="{{if gt .MaxMem 90.0}}metric-high{{else if gt .MaxMem 70.0}}metric-medium{{else}}metric-low{{end}}">{{printf "%.2f" .MaxMem}}%</td>
                <td>{{printf "%.2f" .MinMem}}%</td>
                <td>{{.FirstSeen}}</td>
                <td>{{.LastSeen}}</td>
            </tr>
            {{end}}
        </tbody>
    </table>

    <script>
        let sortDirection = {};
        
        function sortTable(columnIndex) {
            const table = document.getElementById('summaryTable');
            const tbody = table.querySelector('tbody');
            const rows = Array.from(tbody.querySelectorAll('tr')).filter(row => row.style.display !== 'none');
            
            const isNumeric = (str) => {
                if (columnIndex >= 3 && columnIndex <= 8) { // CPU/Memory percentage columns
                    return !isNaN(parseFloat(str.replace('%', '')));
                }
                if (columnIndex === 2) { // Data Points column
                    return !isNaN(parseFloat(str));
                }
                return false;
            };
            
            const getValue = (row, index) => {
                let value = row.cells[index].textContent.trim();
                if (columnIndex >= 3 && columnIndex <= 8) {
                    return parseFloat(value.replace('%', '')) || 0;
                }
                if (columnIndex === 2) {
                    return parseFloat(value) || 0;
                }
                return value.toLowerCase();
            };
            
            const currentDirection = sortDirection[columnIndex] || 'asc';
            const newDirection = currentDirection === 'asc' ? 'desc' : 'asc';
            sortDirection[columnIndex] = newDirection;
            
            rows.sort((a, b) => {
                const aVal = getValue(a, columnIndex);
                const bVal = getValue(b, columnIndex);
                
                let comparison = 0;
                if (aVal < bVal) comparison = -1;
                else if (aVal > bVal) comparison = 1;
                
                return newDirection === 'asc' ? comparison : -comparison;
            });
            
            // Reorder rows
            rows.forEach(row => tbody.appendChild(row));
        }

        function filterTable() {
            const input = document.getElementById('searchInput');
            const filter = input.value.toLowerCase();
            const table = document.getElementById('summaryTable');
            const tbody = table.querySelector('tbody');
            const rows = tbody.querySelectorAll('tr');
            
            rows.forEach(row => {
                const containerName = row.cells[0].textContent.toLowerCase();
                if (containerName.includes(filter)) {
                    row.style.display = '';
                } else {
                    row.style.display = 'none';
                }
            });
        }

        function clearSearch() {
            document.getElementById('searchInput').value = '';
            filterTable();
        }
    </script>
</body>
</html>
`

type PageData struct {
	Files         []StatsFile
	SelectedFile  StatsFile
	SelectedIndex int
}

type SummaryPageData struct {
	Summaries      []ContainerSummary
	TotalFiles     int
	FirstTimestamp string
	LastTimestamp  string
	HighestPeakCPU *ContainerSummary
	MostDataPoints *ContainerSummary
}

func main() {
	// Load all stats files on startup
	statsFiles, err := loadAllStatsFiles("stats/")
	if err != nil {
		log.Fatalf("Error loading stats files: %v", err)
	}

	if len(statsFiles) == 0 {
		log.Fatal("No JSON stats files found in stats/ directory")
	}

	fmt.Printf("Loaded %d stats files\n", len(statsFiles))

	// Create template with custom function
	tmpl := template.Must(template.New("stats").Funcs(template.FuncMap{
		"parseFloat": func(s string) float64 {
			s = strings.TrimSuffix(s, "%")
			val, _ := strconv.ParseFloat(s, 64)
			return val
		},
	}).Parse(htmlTemplate))

	serverData := &ServerData{Files: statsFiles}

	go func() {
		// 5 minute refresh interval
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			// run bash script to refresh stats files
			cmd := exec.Command("bash", "run.sh")
			err := cmd.Run()
			if err != nil {
				log.Printf("Error running run.sh: %v", err)
				continue
			}
			log.Println("Refreshing stats files...")
			newStatsFiles, err := loadAllStatsFiles("stats/")
			if err != nil {
				log.Printf("Error refreshing stats files: %v", err)
				continue
			}
			if len(newStatsFiles) == 0 {
				log.Println("No JSON stats files found in stats/ directory")
				continue
			}
			statsFiles = newStatsFiles
			fmt.Printf("Refreshed %d stats files\n", len(statsFiles))
			// Update server data
			serverData.Files = statsFiles
		}
	}()

	// Main page handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		selectedIndex := 0
		if fileParam := r.URL.Query().Get("file"); fileParam != "" {
			if idx, err := strconv.Atoi(fileParam); err == nil && idx >= 0 && idx < len(statsFiles) {
				selectedIndex = idx
			}
		}

		pageData := PageData{
			Files:         serverData.Files,
			SelectedFile:  serverData.Files[selectedIndex],
			SelectedIndex: selectedIndex,
		}

		w.Header().Set("Content-Type", "text/html")
		if err := tmpl.Execute(w, pageData); err != nil {
			http.Error(w, "Error rendering template", http.StatusInternalServerError)
			log.Printf("Template error: %v", err)
		}
	})

	// API endpoint for container comparison (JSON)
	http.HandleFunc("/api/container/", func(w http.ResponseWriter, r *http.Request) {
		// Extract container ID from URL path
		path := r.URL.Path
		containerID := strings.TrimPrefix(path, "/api/container/")

		if containerID == "" {
			http.Error(w, "Container ID required", http.StatusBadRequest)
			return
		}

		// Get comparison data
		comparison := getContainerComparison(serverData.Files, containerID)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(comparison); err != nil {
			http.Error(w, "Error encoding response", http.StatusInternalServerError)
			log.Printf("JSON encoding error: %v", err)
		}
	})

	// Container details page route
	http.HandleFunc("/container/", func(w http.ResponseWriter, r *http.Request) {
		// Extract container ID from URL path
		path := r.URL.Path
		containerID := strings.TrimPrefix(path, "/container/")

		if containerID == "" {
			http.Error(w, "Container ID required", http.StatusBadRequest)
			return
		}

		// Get comparison data with statistics
		comparison := getContainerComparisonWithStats(serverData.Files, containerID)

		if len(comparison.Data) == 0 {
			http.Error(w, "No historical data found for container", http.StatusNotFound)
			return
		}

		// Render container details page
		containerTmpl := template.Must(template.New("container").Funcs(template.FuncMap{
			"sub": func(a, b int) int {
				return a - b
			},
		}).Parse(containerPageTemplate))
		w.Header().Set("Content-Type", "text/html")
		if err := containerTmpl.Execute(w, comparison); err != nil {
			http.Error(w, "Error rendering template", http.StatusInternalServerError)
			log.Printf("Template error: %v", err)
		}
	})

	// Summary page route
	http.HandleFunc("/summary", func(w http.ResponseWriter, r *http.Request) {
		summaries := getAllContainerSummaries(serverData.Files)

		// Calculate additional stats for summary
		var firstTimestamp, lastTimestamp string
		var highestPeakCPU, mostDataPoints *ContainerSummary

		if len(serverData.Files) > 0 {
			// Sort files by timestamp to get first and last
			sortedFiles := make([]StatsFile, len(serverData.Files))
			copy(sortedFiles, serverData.Files)
			sort.Slice(sortedFiles, func(i, j int) bool {
				return sortedFiles[i].Timestamp.Before(sortedFiles[j].Timestamp)
			})
			firstTimestamp = sortedFiles[0].Timestamp.Format("2006-01-02 15:04:05")
			lastTimestamp = sortedFiles[len(sortedFiles)-1].Timestamp.Format("2006-01-02 15:04:05")
		}

		// Find highest peak CPU and most data points
		for i := range summaries {
			if highestPeakCPU == nil || summaries[i].MaxCPU > highestPeakCPU.MaxCPU {
				highestPeakCPU = &summaries[i]
			}
			if mostDataPoints == nil || summaries[i].DataPoints > mostDataPoints.DataPoints {
				mostDataPoints = &summaries[i]
			}
		}

		pageData := SummaryPageData{
			Summaries:      summaries,
			TotalFiles:     len(serverData.Files),
			FirstTimestamp: firstTimestamp,
			LastTimestamp:  lastTimestamp,
			HighestPeakCPU: highestPeakCPU,
			MostDataPoints: mostDataPoints,
		}

		// Render summary page
		summaryTmpl := template.Must(template.New("summary").Parse(summaryPageTemplate))
		w.Header().Set("Content-Type", "text/html")
		if err := summaryTmpl.Execute(w, pageData); err != nil {
			http.Error(w, "Error rendering template", http.StatusInternalServerError)
			log.Printf("Template error: %v", err)
		}
	})

	port := "8080"
	fmt.Printf("Starting server on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
